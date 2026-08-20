package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionHandlerReturnsBuildMetadataWithoutCaching(t *testing.T) {
	originalCommit, originalBranch, originalTime := buildCommit, buildBranch, buildCommitTime
	t.Cleanup(func() { buildCommit, buildBranch, buildCommitTime = originalCommit, originalBranch, originalTime })
	buildCommit, buildBranch, buildCommitTime = "abc1234", "main", "2026-08-20T10:00:00+08:00"

	recorder := httptest.NewRecorder()
	versionHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var got versionInfo
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if got != (versionInfo{Author: buildAuthor, CommitTime: buildCommitTime, CommitID: buildCommit, Branch: buildBranch}) {
		t.Fatalf("version = %#v", got)
	}
}

func TestCurrentVersionUsesLocalDevelopmentDefaults(t *testing.T) {
	originalCommit, originalBranch, originalTime := buildCommit, buildBranch, buildCommitTime
	t.Cleanup(func() { buildCommit, buildBranch, buildCommitTime = originalCommit, originalBranch, originalTime })
	buildCommit, buildBranch, buildCommitTime = "local-dev", "local-dev", "local-dev"

	if got := currentVersion(); got.CommitID != "local-dev" || got.Branch != "local-dev" || got.CommitTime != "local-dev" {
		t.Fatalf("local development version = %#v", got)
	}
}
