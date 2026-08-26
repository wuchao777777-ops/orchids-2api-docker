package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchids-api/internal/config"
)

func TestConfigureMediaStorageCreatesAndValidatesSharedMount(t *testing.T) {
	oldBase := cacheBaseDir
	t.Cleanup(func() { cacheBaseDir = oldBase })
	directory := t.TempDir()
	cfg := &config.Config{
		StoreMode: "redis", RedisAddr: "redis:6379", MediaDir: directory,
		DeploymentReplicas: 2, DeploymentInstance: "replica-a", DeploymentCluster: "cluster-a", SharedMedia: true,
	}
	if err := ConfigureMediaStorage(cfg); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(directory, sharedMediaMarkerName))
	if err != nil || string(marker) != "cluster-a\n" {
		t.Fatalf("marker=%q err=%v", marker, err)
	}
	if cacheBaseDir != directory {
		t.Fatalf("cacheBaseDir=%q want=%q", cacheBaseDir, directory)
	}
	for _, kind := range []string{"image", "video"} {
		if info, err := os.Stat(filepath.Join(directory, kind)); err != nil || !info.IsDir() {
			t.Fatalf("%s directory missing: %v", kind, err)
		}
	}
	cfg.DeploymentInstance = "replica-b"
	if err := ConfigureMediaStorage(cfg); err != nil {
		t.Fatalf("second replica preflight: %v", err)
	}
	cfg.DeploymentCluster = "cluster-b"
	if err := ConfigureMediaStorage(cfg); err == nil || !strings.Contains(err.Error(), "marker mismatch") {
		t.Fatalf("cluster mismatch error=%v", err)
	}
}

func TestConfigureMediaStorageRejectsIncompleteMultiInstanceConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{name: "redis", cfg: config.Config{DeploymentReplicas: 2, MediaDir: t.TempDir()}, want: "Redis"},
		{name: "instance", cfg: config.Config{DeploymentReplicas: 2, StoreMode: "redis", RedisAddr: "redis:6379", MediaDir: t.TempDir()}, want: "deployment_instance_id"},
		{name: "shared", cfg: config.Config{DeploymentReplicas: 2, StoreMode: "redis", RedisAddr: "redis:6379", DeploymentInstance: "a", DeploymentCluster: "c", MediaDir: t.TempDir()}, want: "shared_media"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ConfigureMediaStorage(&tt.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewHandlerUsesConfiguredDeploymentInstance(t *testing.T) {
	h := NewHandler(&config.Config{DeploymentInstance: " replica-a "}, nil)
	if h.instanceID != "replica-a" {
		t.Fatalf("instanceID=%q want replica-a", h.instanceID)
	}
}
