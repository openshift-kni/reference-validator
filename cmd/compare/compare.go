package compare

import (
	"bytes"
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
	ReferenceDirs  []string
	ResourceDirs   []string
	ExactMatchOnly bool
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
			options.run()

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

	cmd.Flags().BoolVarP(&options.ExactMatchOnly, "exact-match-only", "", false, "Return early by determining if both sets are exact match")

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

func (o compareOptions) run() {
	slog.Info("preparing resources")

	var uListResources []unstructured.Unstructured

	uListResources = readK8sResourcesFromDir(o.ResourceDirs, uListResources)
	uListResources = getResourceFromPolicyIfAny(uListResources)

	slog.Info("preparing reference")

	var uListReference []unstructured.Unstructured

	uListReference = readK8sResourcesFromDir(o.ReferenceDirs, uListReference)
	uListReference = getResourceFromPolicyIfAny(uListReference)

	// short circuit. Useful for ACM vs ZTP cases
	eMatch := equalUnstructuredList(uListResources, uListReference)

	if o.ExactMatchOnly {
		slog.Info("exiting early")

		if eMatch {
			os.Exit(0)
		}

		os.Exit(1)
	}

	resourcesMap := getObjectMetaMap(uListResources)

	slog.Info("----")

	refMap := getObjectMetaMap(uListReference)

	// CRs present in both lists
	intersectionOfSourceList := intersectionOfSources(resourcesMap, refMap)

	// what reference contains but missing in input
	/*dSources := diffOfSources(refMap, resourcesMap)
	slog.Info("not using the following")
	for _, d := range dSources {
		slog.Warn(d.String())
	}*/

	// best case -- exact key
	for _, iSrc := range intersectionOfSourceList {
		curResources := resourcesMap[iSrc]
		curReferences := refMap[iSrc]

		exhaustiveDiff(curResources, curReferences)

		// reduce the user provided the resources
		delete(resourcesMap, iSrc)
	}

	// todo: api version
	// look for partial matches
	slog.Info("attempting to find partial match")

	for key := range resourcesMap {
		equivalentRefKey := findFuzzyMatch(key, refMap)
		curResources := resourcesMap[key]
		curReferences, ok := refMap[equivalentRefKey]

		if !ok {
			msg := fmt.Sprintf("could not find any match for %s", key.String())
			slog.Warn(msg)

			continue
		}

		exhaustiveDiff(curResources, curReferences)
		// reduce the user provided the resources
		delete(resourcesMap, key)
	}

	slog.Info("done")
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

func exhaustiveDiff(resources []unstructured.Unstructured, references []unstructured.Unstructured) {
	temp := make(map[*bytes.Buffer]bool)

	var diffB *bytes.Buffer

	for _, res := range resources {
		for _, ref := range references {
			_, diffReportedPreviously := temp[diffB]
			if !diffReportedPreviously {
				diffB = diffUnstructured(res, ref)
				temp[diffB] = true
			}
			// todo: add more metadata e.g yaml file name
			msg := fmt.Sprintf("seen this diff before, skipping:  %s ", unstructuredToObjMeta(res).String())
			slog.Info(msg)
		}
	}
}

func diffUnstructured(res unstructured.Unstructured, ref unstructured.Unstructured) *bytes.Buffer {
	// move this outside
	diff := k8sdiff.DiffProgram{
		Exec:      exec.New(),
		IOStreams: genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr},
	}

	i, _, myOut, _ := genericiooptions.NewTestIOStreams()
	diff2 := k8sdiff.DiffProgram{
		Exec:      exec.New(),
		IOStreams: i,
	}

	resPath := unstructuredToYaml(res)
	refPath := unstructuredToYaml(ref)

	err := diff.Run(resPath, refPath)
	_ = diff2.Run(resPath, refPath)

	if err == nil {
		msg := fmt.Sprintf("res: %s and ref: %s are exact same", unstructuredToObjMeta(res).String(), unstructuredToObjMeta(ref).String())
		slog.Info(msg)
	}

	os.RemoveAll(resPath)
	os.RemoveAll(refPath)

	return myOut
}

func unstructuredToYaml(uStructured unstructured.Unstructured) string {
	dir, err := os.MkdirTemp("", uStructured.GetName())
	if err != nil {
		log.Fatal(err)
	}

	content, err := yaml.Marshal(uStructured.UnstructuredContent())
	if err != nil {
		return ""
	}

	file := filepath.Join(dir, unstructuredToObjMeta(uStructured).String())
	permission := 0o600

	if err := os.WriteFile(file, content, os.FileMode(permission)); err != nil {
		log.Fatal(err)
	}

	return file
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

func getResourceFromPolicyIfAny(uList []unstructured.Unstructured) []unstructured.Unstructured {
	// Extract the main CR if policy
	var uListWithoutP []unstructured.Unstructured

	for _, curUnstructured := range uList {
		if curUnstructured.GetKind() == "Policy" {
			policy := policyv1.Policy{}

			err := runtime.DefaultUnstructuredConverter.FromUnstructured(curUnstructured.Object, &policy)
			if err != nil {
				slog.Warn("invalid Policy CR")

				continue
			}

			uListWithoutP = append(uListWithoutP, getObjectTemplates(policy)...)

			continue
		}

		uListWithoutP = append(uListWithoutP, curUnstructured)
	}

	return uListWithoutP
}

func readK8sResourcesFromDir(curDir []string, uList []unstructured.Unstructured) []unstructured.Unstructured {
	for _, d := range curDir {
		files, _ := util.GetFileNames(d)
		for _, f := range files {
			u := yamlToUnstructured(f)
			if u != nil {
				uList = append(uList, *u)
			}
		}
	}

	return uList
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

func equalUnstructuredList(setA []unstructured.Unstructured, setB []unstructured.Unstructured) bool {
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
