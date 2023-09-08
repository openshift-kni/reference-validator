package compare

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	k8sdiff "k8s.io/kubectl/pkg/cmd/diff"
	"k8s.io/utils/exec"
	"sigs.k8s.io/cli-utils/pkg/object"
)

func Test_yamlToUnstructured(t *testing.T) {
	const resourceNs = `
apiVersion: v1
kind: NamespaceExpect
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
`

	type args struct {
		file string
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "pass in a CR and get back Unstructured",
			args: args{file: mustGetTestFilePath(t, resourceNs)},
			want: "NamespaceExpect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := yamlToUnstructured(tt.args.file); !reflect.DeepEqual(got.GetKind(), tt.want) {
				t.Errorf("yamlToUnstructured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mustGetTestFilePath(t *testing.T, content string) string {
	t.Helper()
	testDir := t.TempDir()
	file, _ := os.CreateTemp(testDir, "testfile")

	_, err := file.WriteString(content)
	if err != nil {
		panic("could not create file")
	}

	return file.Name()
}

func Test_compareOptions_diffUnstructured(t *testing.T) {
	const resCR1 = `
apiVersion: v1
kind: NamespaceExpect
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
`

	const refCR1 = `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
`

	const resListCR1 = `
apiVersion: v1
kind: NamespaceExpect
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
  list:
    - one 
    - two
`

	refListCR1 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
  list:
    - one 
    - five
`

	i, _, _, _ := genericiooptions.NewTestIOStreams()

	type fields struct {
		Diff *k8sdiff.DiffProgram
	}

	type args struct {
		res unstructured.Unstructured
		ref unstructured.Unstructured
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		wantDiff bool
	}{
		{
			name: "A and b identical except for field in different order",
			fields: fields{
				Diff: &k8sdiff.DiffProgram{
					Exec:      exec.New(),
					IOStreams: i,
				},
			},
			args: args{
				res: *yamlToUnstructured(mustGetTestFilePath(t, resCR1)),
				ref: *yamlToUnstructured(mustGetTestFilePath(t, refCR1)),
			},
			wantDiff: false,
		},
		{
			name: "A and b identical except list entries are different",
			fields: fields{
				Diff: &k8sdiff.DiffProgram{
					Exec:      exec.New(),
					IOStreams: i,
				},
			},
			args: args{
				res: *yamlToUnstructured(mustGetTestFilePath(t, resListCR1)),
				ref: *yamlToUnstructured(mustGetTestFilePath(t, refListCR1)),
			},
			wantDiff: true,
		},
		{
			name: "A and b identical (including list)",
			fields: fields{
				Diff: &k8sdiff.DiffProgram{
					Exec:      exec.New(),
					IOStreams: i,
				},
			},
			args: args{
				res: *yamlToUnstructured(mustGetTestFilePath(t, resListCR1)),
				ref: *yamlToUnstructured(mustGetTestFilePath(t, resListCR1)),
			},
			wantDiff: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := compareOptions{
				Diff: tt.fields.Diff,
			}
			isDiff := o.diffUnstructured(tt.args.res, tt.args.ref)

			if !tt.wantDiff && isDiff != nil {
				t.Errorf(isDiff.Error())

				return
			}
		})
	}
}

func Test_compareOptions_run(t *testing.T) { //nolint:golint,gocognit
	cr1 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
`
	cr2 := `
apiVersion: v1
kind: NamespaceCustom
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
`
	cr3 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
`

	type fields struct {
		ReferenceDirs []string
		ResourceDirs  []string
		Diff          *k8sdiff.DiffProgram
	}

	tests := []struct {
		name              string
		fields            fields
		wantResourceKind  string
		wantResourceCount int
		wantRefCount      int
		wantErr           bool
	}{
		{
			name: "User provides an extra resource not present in reference",
			fields: fields{
				ReferenceDirs: []string{filepath.Dir(mustGetTestFilePath(t, cr3))},
				ResourceDirs:  []string{filepath.Dir(mustGetTestFilePath(t, cr1)), filepath.Dir(mustGetTestFilePath(t, cr2))},
			},
			wantResourceKind:  "NamespaceCustom",
			wantResourceCount: 1,
		},
		{
			name: "User did not use all the refer",
			fields: fields{
				ReferenceDirs: []string{filepath.Dir(mustGetTestFilePath(t, cr1)), filepath.Dir(mustGetTestFilePath(t, cr2))},
				ResourceDirs:  []string{filepath.Dir(mustGetTestFilePath(t, cr3))},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := compareOptions{
				ReferenceDirs: tt.fields.ReferenceDirs,
				ResourceDirs:  tt.fields.ResourceDirs,
				Diff:          tt.fields.Diff,
			}
			resourceMap, _, err := o.run()
			if tt.wantResourceKind != "" {
				for _, value := range resourceMap {
					if tt.wantResourceCount != len(value) {
						t.Errorf("unexpected count")
					}
					if tt.wantResourceCount == 1 {
						if tt.wantResourceKind != value[0].GetKind() {
							t.Errorf("unexpected kind")
						}
					}
				}

				return
			}

			if tt.wantErr {
				if err == nil {
					t.Errorf("error expected")
				} else {
					msg := fmt.Sprintf("got: %s", err.Error())
					slog.Info(msg)
				}

				return
			}
		})
	}
}

func Test_findFuzzyMatch(t *testing.T) {
	cr4FuzzyClose := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28closeEnough
`
	cr4FuzzyNotClose := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28closeNotEnough
`
	cr5Fuzzy := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
`

	type args struct {
		key    object.ObjMetadata
		refMap map[object.ObjMetadata][]unstructured.Unstructured
	}

	tests := []struct {
		name string
		args args
		want object.ObjMetadata
	}{
		{
			name: "find the nearest CR",
			args: args{
				key: unstructuredToObjMeta(*yamlToUnstructured(mustGetTestFilePath(t, cr5Fuzzy))),
				refMap: map[object.ObjMetadata][]unstructured.Unstructured{
					unstructuredToObjMeta(*yamlToUnstructured(mustGetTestFilePath(t, cr4FuzzyClose))):    {},
					unstructuredToObjMeta(*yamlToUnstructured(mustGetTestFilePath(t, cr4FuzzyNotClose))): {},
				},
			},
			want: unstructuredToObjMeta(*yamlToUnstructured(mustGetTestFilePath(t, cr4FuzzyClose))),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findFuzzyMatch(tt.args.key, tt.args.refMap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("findFuzzyMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_compareOptions_keyExactMatch(t *testing.T) {
	cr1 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
spec:
  content:
    this is cr1
`
	cr2 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
spec:
  content:
    this is cr2
`
	cr3 := `
apiVersion: v1
kind: NamespaceExpect
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
spec:
  content:
    this is cr3
`
	i, _, _, _ := genericiooptions.NewTestIOStreams()

	type fields struct {
		Diff *k8sdiff.DiffProgram
	}

	type args struct {
		resourcesMap map[object.ObjMetadata][]unstructured.Unstructured
		refMap       map[object.ObjMetadata][]unstructured.Unstructured
	}

	tests := []struct {
		name      string
		fields    fields
		args      args
		diffCount int
	}{
		{
			name: "Co-relating with multiple potential match",
			args: args{
				resourcesMap: getObjectMetaMap([]unstructured.Unstructured{*yamlToUnstructured(mustGetTestFilePath(t, cr1))}),
				refMap:       getObjectMetaMap([]unstructured.Unstructured{*yamlToUnstructured(mustGetTestFilePath(t, cr2)), *yamlToUnstructured(mustGetTestFilePath(t, cr3))}),
			},
			fields: fields{Diff: &k8sdiff.DiffProgram{
				Exec:      exec.New(),
				IOStreams: i,
			}},
			diffCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := compareOptions{
				Diff: tt.fields.Diff,
			}
			gotDiffcount := o.keyExactMatch(tt.args.resourcesMap, tt.args.refMap)
			if tt.diffCount != len(gotDiffcount) {
				t.Errorf("keyExactMatch() = %v, want %v", len(gotDiffcount), tt.diffCount)
			}
		})
	}
}
