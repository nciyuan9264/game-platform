package configvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeploymentYAMLParses(t *testing.T) {
	files := []string{
		"docker-compose.center.prod.yml",
		"docker-compose.games.prod.yml",
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

func TestDeploymentConfigBoundaries(t *testing.T) {
	center, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.center.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	games, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.games.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(center), "ROOMHOST_") {
		t.Fatal("center production config depends on RoomHost artifacts")
	}
	for _, service := range []string{"schema-migrate:", "control-plane:", "platform-rpc:", "admin-rpc:", "gateway:"} {
		if strings.Contains(string(games), "\n  "+service) {
			t.Fatalf("games production config defines center service %s", service)
		}
	}
}

func TestDeployWorkflowSupportsAdminRPCOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "deploy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	_, centerJob, found := strings.Cut(workflow, "\n  deploy-center:")
	if !found {
		t.Fatal("deploy workflow missing center job")
	}
	centerJob, _, found = strings.Cut(centerJob, "\n  deploy-games:")
	if !found {
		t.Fatal("deploy workflow missing games job")
	}
	if strings.Contains(centerJob, "ROOMHOST_") {
		t.Fatal("center deploy job depends on RoomHost artifacts")
	}
	for _, required := range []string{
		"- admin-rpc",
		"admin-rpc)",
		`center='["admin-rpc"]'`,
		`games='[]'`,
		"source: docker-compose.center.prod.yml",
		"source: docker-compose.games.prod.yml",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("deploy workflow missing %q", required)
		}
	}
}
