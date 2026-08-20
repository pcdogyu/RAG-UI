// Research OS 的 Go 服务入口。
// 前端通过 /api/v1/* 与本服务通信；生产环境只需在此替换 demo 数据为 WeKnora API 适配器。
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type report struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Asset    string `json:"asset"`
	Status   string `json:"status"`
	Deadline string `json:"deadline"`
}

type askRequest struct {
	Question string `json:"question"`
	Scope    string `json:"scope"`
}

const buildAuthor = "Yuhao@jiansutech.com"

// These values are set by the production build through -ldflags. Keeping a
// clear local-dev value makes development builds honest instead of guessing.
var (
	buildCommit     = "local-dev"
	buildBranch     = "local-dev"
	buildCommitTime = "local-dev"
)

type versionInfo struct {
	Author     string `json:"author"`
	CommitTime string `json:"commit_time"`
	CommitID   string `json:"commit_id"`
	Branch     string `json:"branch"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func currentVersion() versionInfo {
	return versionInfo{Author: buildAuthor, CommitTime: buildCommitTime, CommitID: buildCommit, Branch: buildBranch}
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, currentVersion())
}

func main() {
	mux := http.NewServeMux()
	weKnora := NewWeKnoraClient(loadWeKnoraConfig())
	liquidationStore, err := openLiquidationStore(context.Background(), os.Getenv("RAG_UI_DATABASE_URL"))
	if err != nil {
		log.Printf("liquidation database disabled: %v", err)
	}
	if liquidationStore != nil {
		defer liquidationStore.Close()
	}
	liquidations := newLiquidationService(liquidationStore)
	liquidations.start(context.Background())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "research-os"})
	})
	mux.HandleFunc("GET /api/v1/version", versionHandler)
	mux.HandleFunc("GET /api/v1/liquidations/symbols", liquidations.serveSymbols)
	mux.HandleFunc("GET /api/v1/liquidations/status", liquidations.serveStatus)
	mux.HandleFunc("GET /api/v1/liquidations/chart", liquidations.serveChart)
	mux.HandleFunc("GET /api/v1/liquidations/stream", liquidations.serveLiveWS)
	mux.HandleFunc("GET /api/v1/research/reports", func(w http.ResponseWriter, _ *http.Request) {
		// TODO(weknora): 由知识库检索与机构语义库组合查询替换。
		writeJSON(w, http.StatusOK, []report{
			{ID: "eth-0810", Title: "ETH 8 月综合研判", Asset: "ETH", Status: "当前有效", Deadline: "2026-08-10"},
			{ID: "nvda-0808", Title: "NVDA 财报前预期差跟踪", Asset: "NVDA", Status: "当前有效", Deadline: "2026-08-08"},
		})
	})
	mux.HandleFunc("GET /api/v1/research/uploads", weKnora.serveResearchUploads)
	mux.HandleFunc("POST /api/v1/research/uploads", weKnora.serveResearchUpload)
	mux.HandleFunc("POST /api/v1/research/ask", func(w http.ResponseWriter, r *http.Request) {
		var request askRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || strings.TrimSpace(request.Question) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
			return
		}
		answer, err := weKnora.Ask(r.Context(), request.Question, request.Scope)
		if err != nil {
			if !weKnora.config.enabled() {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WeKnora is not configured; set WEKNORA_EMAIL and WEKNORA_PASSWORD"})
				return
			}
			log.Printf("WeKnora ask failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "WeKnora research agent did not return an answer"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"scope":  request.Scope,
			"answer": answer,
			"source": "weknora-agent",
		})
	})

	// 开发时由 Vite 提供页面；构建后 Go 可独立托管 dist 目录。
	dist := filepath.Join("dist")
	if info, err := os.Stat(dist); err == nil && info.IsDir() {
		files := http.FileServer(http.Dir(dist))
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				if _, err := os.Stat(filepath.Join(dist, filepath.Clean(r.URL.Path))); err == nil {
					files.ServeHTTP(w, r)
					return
				}
			}
			http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		})
	}

	address := os.Getenv("RESEARCH_OS_ADDR")
	if address == "" {
		address = ":80"
	}
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Research OS listening on %s", address)
	log.Fatal(server.ListenAndServe())
}
