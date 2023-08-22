package compare

import (
	"os"
	"reflect"
	"testing"
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

func mustGetTestFilePath(t *testing.T, cr string) string {
	t.Helper()
	testDir := t.TempDir()
	file, _ := os.CreateTemp(testDir, "testfile")

	_, err := file.WriteString(cr)
	if err != nil {
		panic("could not create file")
	}

	return file.Name()
}
