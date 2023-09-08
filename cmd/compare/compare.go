package compare

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hbollon/go-edlib"
	"github.com/openshift-kni/reference-validator/pkg/util"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	k8sdiff "k8s.io/kubectl/pkg/cmd/diff"
	"k8s.io/utils/exec"
	configurationPolicyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/cli-utils/pkg/object"
)

type compareOptions struct {
	ReferenceDirs []string
	ResourceDirs  []string
	Diff          *k8sdiff.DiffProgram
}

func NewCmdCompare() *cobra.Command {
	options := &compareOptions{}

	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare two sets of k8s resources",
		Long:  `Compare two sets of k8s resources using two directory paths`,
		Args:  cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := options.validate(); err != nil {
				slog.Error("could not validate input")

				return err
			}
			options.run() //nolint:golint,errcheck

			return nil
		},
	}

	// flags
	cmd.Flags().StringSliceVarP(&options.ReferenceDirs, "reference", "", []string{}, "Reference configuration directory such as source-cr directory from ZTP")

	err := cmd.MarkFlagRequired("reference")
	if err != nil {
		return nil
	}

	cmd.Flags().StringSliceVarP(&options.ResourceDirs, "resource", "", []string{}, "User configuration directory to read from")

	err = cmd.MarkFlagRequired("resource")
	if err != nil {
		return nil
	}

	return cmd
}

func (o compareOptions) validate() error {
	for _, dir := range o.ReferenceDirs {
		if !util.IsDirectory(dir) {
			return errors.New("all Reference paths must be a directory")
		}
	}

	for _, dir := range o.ResourceDirs {
		if !util.IsDirectory(dir) {
			return errors.New("all Resource paths must be a directory")
		}
	}

	return nil
}

func (o compareOptions) run() (map[object.ObjMetadata][]unstructured.Unstructured, map[object.ObjMetadata][]unstructured.Unstructured, error) { //nolint:golint,unparam
	slog.Info("preparing resources")

	uListResources := readK8sResourcesFromDir(o.ResourceDirs)

	slog.Info("preparing reference")

	uListReference := readK8sResourcesFromDir(o.ReferenceDirs)

	// short circuit. Useful for ACM vs ZTP cases
	eMatch := contentExactMatch(uListResources, uListReference)
	if eMatch {
		slog.Info("two sets exact match")
		os.Exit(0)
	}

	resourcesMap := getObjectMetaMap(uListResources)

	slog.Info("----")

	refMap := getObjectMetaMap(uListReference)

	// CRs present in both lists
	// best case -- exact key
	o.keyExactMatch(resourcesMap, refMap)

	// todo: api version
	// look for partial matches
	slog.Info("attempting to find partial match")

	o.keyPartialMatch(resourcesMap, refMap)

	// warning no match for user provided CRs
	if len(resourcesMap) > 0 {
		slog.Warn("could not find any match for the following")

		for _, value := range resourcesMap {
			for _, curU := range value {
				_, content := unstructuredToYaml(curU)

				msg := fmt.Sprintf("\n%s", content)
				slog.Warn(msg)
			}
		}
	}

	// error when reference CRs are not used
	if len(refMap) > 0 {
		slog.Error("unused reference CR")

		for _, value := range refMap {
			for _, curU := range value {
				_, content := unstructuredToYaml(curU)

				msg := fmt.Sprintf("\n%s", content)
				slog.Error(msg)
			}
		}

		err := errors.New("reference CRs are not used")

		return resourcesMap, refMap, err
	}

	return resourcesMap, refMap, nil
}

func (o compareOptions) keyPartialMatch(resourcesMap map[object.ObjMetadata][]unstructured.Unstructured, refMap map[object.ObjMetadata][]unstructured.Unstructured) []error {
	var errs []error

	for key := range resourcesMap {
		equivalentRefKey := findFuzzyMatch(key, refMap)
		curResources := resourcesMap[key]
		curReferences, ok := refMap[equivalentRefKey]

		if !ok {
			msg := fmt.Sprintf("could not find any match for %s", key.String())
			slog.Warn(msg)

			continue
		}

		errs = append(errs, o.exhaustiveDiff(curResources, curReferences)...)
		// reduce the user provided the resources
		delete(resourcesMap, key)
		delete(refMap, key)
	}

	return errs
}

func (o compareOptions) keyExactMatch(resourcesMap map[object.ObjMetadata][]unstructured.Unstructured, refMap map[object.ObjMetadata][]unstructured.Unstructured) []error {
	intersectionOfSourceList := intersectionOfSources(resourcesMap, refMap)

	var errs []error

	for _, iSrc := range intersectionOfSourceList {
		curResources := resourcesMap[iSrc]
		curReferences := refMap[iSrc]

		errs = append(errs, o.exhaustiveDiff(curResources, curReferences)...)

		// reduce the user provided the resources
		delete(resourcesMap, iSrc)
		delete(refMap, iSrc)
	}

	return errs
}

func findFuzzyMatch(key object.ObjMetadata, refMap map[object.ObjMetadata][]unstructured.Unstructured) object.ObjMetadata {
	var allKeysString []string

	for k := range refMap {
		allKeysString = append(allKeysString, k.String())
	}

	matchWith := key.String()
	threshold := 0.5
	numOfResults := 3

	res, err := edlib.FuzzySearchSetThreshold(matchWith, allKeysString, numOfResults, float32(threshold), edlib.Levenshtein)
	if err != nil {
		return object.NilObjMetadata
	}

	fmt.Printf("with '%f' threshold --> Results: %s, for Key: %s\n", float32(threshold), strings.Join(res, ", "), matchWith)
	o, _ := object.ParseObjMetadata(res[0])

	return o
}

func (o compareOptions) exhaustiveDiff(resources []unstructured.Unstructured, references []unstructured.Unstructured) []error {
	var errs []error

	for _, res := range resources {
		for _, ref := range references {
			errs = append(errs, o.diffUnstructured(res, ref))
		}
	}

	return errs
}

func (o compareOptions) diffUnstructured(res unstructured.Unstructured, ref unstructured.Unstructured) error {
	if o.Diff == nil {
		o.Diff = &k8sdiff.DiffProgram{
			Exec:      exec.New(),
			IOStreams: genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr},
		}
	}

	resPath, _ := unstructuredToYaml(res)
	refPath, _ := unstructuredToYaml(ref)

	diffFound := o.Diff.Run(resPath, refPath)

	if diffFound == nil {
		msg := fmt.Sprintf("res: %s and ref: %s are exact same", unstructuredToObjMeta(res).String(), unstructuredToObjMeta(ref).String())
		slog.Info(msg)
	}

	os.RemoveAll(resPath)
	os.RemoveAll(refPath)

	return diffFound
}

func unstructuredToYaml(uStructured unstructured.Unstructured) (string, string) {
	dir, err := os.MkdirTemp("", uStructured.GetName())
	if err != nil {
		log.Fatal(err)
	}

	content, err := yaml.Marshal(uStructured.UnstructuredContent())
	if err != nil {
		return "", ""
	}

	file := filepath.Join(dir, unstructuredToObjMeta(uStructured).String())
	permission := 0o600

	if err := os.WriteFile(file, content, os.FileMode(permission)); err != nil {
		log.Fatal(err)
	}

	return file, string(content)
}

func intersectionOfSources(mapA, mapB map[object.ObjMetadata][]unstructured.Unstructured) object.ObjMetadataSet {
	setA := objMetadataSetFromMap(mapA)
	setB := objMetadataSetFromMap(mapB)

	val := setA.Intersection(setB)

	for _, v := range val {
		slog.Info(v.String())
	}

	return val
}

// objMetadataSetFromMap constructs a set from a map.
func objMetadataSetFromMap(mapA map[object.ObjMetadata][]unstructured.Unstructured) object.ObjMetadataSet {
	setA := make(object.ObjMetadataSet, 0, len(mapA))
	for f := range mapA {
		setA = append(setA, f)
	}

	return setA
}

func getObjectMetaMap(uListUnstructured []unstructured.Unstructured) map[object.ObjMetadata][]unstructured.Unstructured {
	curMap := make(map[object.ObjMetadata][]unstructured.Unstructured)

	for _, u := range uListUnstructured {
		key := unstructuredToObjMeta(u)
		curMap[key] = append(curMap[key], u)
	}

	return curMap
}

// UnstructuredToObjMeta extracts the object metadata information from a unstructured.Unstructured and returns it as ObjMetadata.
func unstructuredToObjMeta(obj unstructured.Unstructured) object.ObjMetadata {
	newID := object.ObjMetadata{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		GroupKind: obj.GetObjectKind().GroupVersionKind().GroupKind(),
	}

	return newID
}

func getResourcesFromPolicyIfAny(curUnstructured unstructured.Unstructured) []unstructured.Unstructured {
	// Extract the main CR if policy
	var uListWithoutP []unstructured.Unstructured

	if curUnstructured.GetKind() == "Policy" {
		policy := policyv1.Policy{}

		err := runtime.DefaultUnstructuredConverter.FromUnstructured(curUnstructured.Object, &policy)
		if err != nil {
			slog.Warn("invalid Policy CR")

			return nil
		}

		return append(uListWithoutP, getObjectTemplates(policy)...)
	}

	uListWithoutP = append(uListWithoutP, curUnstructured)

	return uListWithoutP
}

func readK8sResourcesFromDir(curDir []string) []unstructured.Unstructured {
	var finalList []unstructured.Unstructured

	removeDuplicate := make(map[string]string)

	for _, d := range curDir {
		files, _ := util.GetFileNames(d)
		for _, curFile := range files {
			u := yamlToUnstructured(curFile)
			uList := getResourcesFromPolicyIfAny(*u)

			// post process
			uList = removeDuplicates(uList, removeDuplicate, curFile)
			uList = removeResourcesWeDontWantToProcess(uList)

			finalList = append(finalList, uList...)
		}
	}

	return finalList
}

func removeResourcesWeDontWantToProcess(uList []unstructured.Unstructured) []unstructured.Unstructured { //nolint:golint,cyclop
	var finalList []unstructured.Unstructured
	// todo: refer to reference dir to dynamically create these rules?
	for _, u := range uList {
		if u.GetAPIVersion() == "rbac.authorization.k8s.io/v1" ||
			u.GetAPIVersion() == "SecurityContextConstraints-security.openshift.io" ||
			u.GetAPIVersion() == "config.openshift.io/v1" ||
			u.GetAPIVersion() == "security.openshift.io/v1" ||
			u.GetAPIVersion() == "sriovfec.intel.com/v2" || // it's optional?!
			u.GetObjectKind().GroupVersionKind().Kind == "Secret" ||
			u.GetObjectKind().GroupVersionKind().Kind == "Namespace" ||
			u.GetObjectKind().GroupVersionKind().Kind == "MachineConfigPool" ||
			u.GetObjectKind().GroupVersionKind().Kind == "ServiceAccount" ||
			u.GetObjectKind().GroupVersionKind().Kind == "Node" ||
			u.GetObjectKind().GroupVersionKind().Kind == "PlacementBinding" ||
			u.GetObjectKind().GroupVersionKind().Kind == "PlacementRule" {
			continue
		}

		finalList = append(finalList, u)
	}

	return finalList
}

func removeDuplicates(uList []unstructured.Unstructured, removeDuplicate map[string]string, curFile string) []unstructured.Unstructured {
	var finalList []unstructured.Unstructured

	for _, curU := range uList {
		key, _ := curU.MarshalJSON()

		if seenBeforeFilePath, seenBefore := removeDuplicate[string(key)]; seenBefore {
			msg := fmt.Sprintf("previously seen full or partial content of %s in %s", curFile, seenBeforeFilePath)
			slog.Warn(msg)

			continue
		}

		removeDuplicate[string(key)] = curFile

		finalList = append(finalList, curU)
	}

	return finalList
}

func yamlToUnstructured(file string) *unstructured.Unstructured {
	yFile, _ := os.ReadFile(file)
	newUnstructured := &unstructured.Unstructured{Object: map[string]interface{}{}}

	if err := yaml.Unmarshal(yFile, &newUnstructured.Object); err != nil {
		slog.Warn(fmt.Sprintf("could not convert %s to Unstructured, skipping", file))

		return nil
	}

	return newUnstructured
}

func getConfigurationPolicy(p policyv1.Policy) []configurationPolicyv1.ConfigurationPolicy {
	var cPs []configurationPolicyv1.ConfigurationPolicy

	for _, policyTemplate := range p.Spec.PolicyTemplates {
		uConfigPolicy := &unstructured.Unstructured{}

		err := uConfigPolicy.UnmarshalJSON(policyTemplate.ObjectDefinition.Raw)
		if err != nil {
			slog.Warn("could not unmarshal unstructured ConfigPolicy")

			continue
		}

		tConfigPolicy := configurationPolicyv1.ConfigurationPolicy{}

		err = runtime.DefaultUnstructuredConverter.FromUnstructured(uConfigPolicy.UnstructuredContent(), &tConfigPolicy)
		if err != nil {
			slog.Warn("could not convert unstructured ConfigPolicy to typed ConfigPolicy")

			continue
		}

		cPs = append(cPs, tConfigPolicy)
	}

	return cPs
}

func getObjectTemplates(p policyv1.Policy) []unstructured.Unstructured {
	slog.Info(fmt.Sprintf("extracting %s --->", p.Name))
	cPolicies := getConfigurationPolicy(p)

	var objT []unstructured.Unstructured

	for _, cPolicy := range cPolicies {
		for _, ot := range cPolicy.Spec.ObjectTemplates {
			customResource := &unstructured.Unstructured{}

			err := customResource.UnmarshalJSON(ot.ObjectDefinition.Raw)
			if err != nil {
				slog.Warn("could not convert raw ObjectTemplates to unstructured ObjectTemplates")

				continue
			}

			objT = append(objT, *customResource)
		}
	}

	return objT
}

func contentExactMatch(setA []unstructured.Unstructured, setB []unstructured.Unstructured) bool {
	mapA := make(map[string]string, len(setA))

	for _, a := range setA {
		jsonBytes, err := a.MarshalJSON()
		if err != nil {
			mapA[string(jsonBytes)] = err.Error()
		} else {
			mapA[string(jsonBytes)] = ""
		}
	}

	mapB := make(map[string]string, len(setB))

	for _, b := range setB {
		jsonBytes, err := b.MarshalJSON()
		if err != nil {
			mapB[string(jsonBytes)] = err.Error()
		} else {
			mapB[string(jsonBytes)] = ""
		}
	}

	if len(mapA) != len(mapB) {
		return false
	}

	for b, errB := range mapB {
		if errA, exists := mapA[b]; !exists {
			if !exists {
				return false
			}

			if errA != errB {
				// might never reach here...
				return false
			}
		}
	}

	return true
}
