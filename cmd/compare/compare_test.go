package compare

import (
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	k8sdiff "k8s.io/kubectl/pkg/cmd/diff"
	"k8s.io/utils/exec"
)

func Test_yamlToUnstructured(t *testing.T) {
	resourceNs := `
apiVersion: v1
kind: Namespace
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
			want: "Namespace",
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
	resCR1 := `
apiVersion: v1
kind: Namespace
metadata:
  name: cnfdf28
  labels:
    name: cnfdf28
`
	refCR1 := `
apiVersion: v1
kind: Namespace
metadata:
  labels:
    name: cnfdf28
  name: cnfdf28
`

	resListCR1 := `
apiVersion: v1
kind: Namespace
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
kind: Namespace
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
