package configvalidation

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeploymentYAMLParses(t *testing.T) {
	files := []string{
		"docker-compose.prod.yml",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "workflows", "deploy.yml"),
	}
	for _, relative := range files {
		t.Run(relative, func(t *testing.T) {
			path := filepath.Join("..", "..", relative)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document yaml.Node
			if err := yaml.Unmarshal(data, &document); err != nil {
				t.Fatalf("parse %s: %v", relative, err)
			}
		})
	}
}
