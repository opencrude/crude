package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const catalogRouterFixture = `{"data":[{"id":"vendor/vision","context_length":128000,"architecture":{"input_modalities":["text","image"]},"top_provider":{"max_completion_tokens":8192},"supported_parameters":["tools"],"pricing":{"prompt":"0.000001","completion":"0.000003"}},{"id":"unused/model","architecture":{"input_modalities":["text"]}}]}`
const catalogModelsDevFixture = `{"vendor":{"models":{"vision":{"limit":{"context":64000,"output":4096},"modalities":{"input":["text"]},"tool_call":true},"unused":{"limit":{"context":32000}}}}}`
const catalogLiteLLMFixture = `{"sample_spec":{"max_input_tokens":"documentation"},"vision":{"max_input_tokens":96000,"max_output_tokens":6000,"supports_vision":true,"supports_audio_input":true,"supports_function_calling":true},"unconfigured-model":{"max_input_tokens":16000}}`

func TestCatalogSourcesPreserveUnknownAndExplicitFalse(t *testing.T) {
	for _, tc := range []struct{ format, body, model string }{
		{"openrouter", catalogRouterFixture, "vendor/vision"},
		{"models.dev", catalogModelsDevFixture, "vision"},
		{"litellm", catalogLiteLLMFixture, "vision"},
	} {
		models, err := parseModelSource(tc.format, []byte(tc.body))
		if err != nil || len(models) < 2 {
			t.Fatalf("%s did not retain the full dataset: %+v, %v", tc.format, models, err)
		}
		meta := models[tc.model]
		if tc.format == "litellm" {
			if _, known := meta.ModalitySupport[ModalityPDF]; known {
				t.Fatal("omitted LiteLLM PDF support became a fabricated No")
			}
		} else if supported, known := meta.ModalitySupport[ModalityPDF]; supported || !known {
			t.Fatalf("%s lost the complete model-card input declaration", tc.format)
		}
	}
	for _, data := range []string{`{`, `{}`, `{"data":[]}`} {
		if _, err := parseModelSource("openrouter", []byte(data)); err == nil {
			t.Fatal("invalid/empty snapshot was accepted")
		}
	}
	models, err := parseModelSource("openrouter", []byte(`{"data":[{"id":"tiered","pricing":{"prompt":"0.001","completion":"0.002","tiers":[{"tokens":200000,"rate":"0.003"}]}}]}`))
	if err != nil || !models["tiered"].PricingKnown {
		t.Fatalf("new pricing fields broke otherwise valid public model records: %v", err)
	}
}

func TestCatalogPersistsAllSourcesAndLoadsOfflineWithProvenance(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("public catalog sent account credentials")
		}
		w.Header().Set("ETag", `"fixture"`)
		if r.Header.Get("If-None-Match") == `"fixture"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body := map[string]string{"/router": catalogRouterFixture, "/models": catalogModelsDevFixture, "/litellm": catalogLiteLLMFixture}[r.URL.Path]
		_, _ = fmt.Fprint(w, body)
	}))
	sources := []ModelSource{{"openrouter", server.URL + "/router", "openrouter"}, {"models.dev", server.URL + "/models", "models.dev"}, {"litellm", server.URL + "/litellm", "litellm"}}
	path := filepath.Join(t.TempDir(), "model-catalog.db")
	catalog, err := OpenModelCatalog(path, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if err := catalog.refresh(context.Background(), source); err != nil {
			t.Fatal(err)
		}
	}
	meta, ok := catalog.Lookup("vendor/vision")
	if !ok || meta.ContextWindow != 128000 || meta.MaxOutput != 8192 || !meta.ModalitySupport[ModalityImage] || meta.ModalitySupport[ModalityAudio] || !slices.Contains(meta.ConflictingModalities, ModalityImage) || !slices.Contains(meta.ConflictingModalities, ModalityAudio) || !slices.Equal(meta.Sources, []string{"openrouter", "models.dev", "litellm"}) {
		t.Fatalf("catalog lost capability provenance or conflict evidence: %+v", meta)
	}
	if _, ok := catalog.Lookup("unconfigured-model"); !ok {
		t.Fatal("catalog only saved configured/selected models")
	}
	var raw []byte
	if err := catalog.db.QueryRow(`SELECT body FROM sources WHERE id='litellm'`).Scan(&raw); err != nil || string(raw) != catalogLiteLLMFixture {
		t.Fatal("downloaded source dataset was not persisted")
	}
	generation := catalog.snapshot.Load().generation
	if err := catalog.refresh(context.Background(), sources[0]); err != nil {
		t.Fatal(err)
	}
	if catalog.snapshot.Load().generation != generation {
		t.Fatal("HTTP 304 invalidated the metadata snapshot")
	}
	meta.ModalitySupport[ModalityImage] = false
	meta.Sources[0] = "mutated"
	unchanged, _ := catalog.Lookup("vendor/vision")
	if !unchanged.ModalitySupport[ModalityImage] || unchanged.Sources[0] != "openrouter" {
		t.Fatal("catalog exposed mutable snapshot internals")
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	server.Close()
	before := requests.Load()
	reopened, err := OpenModelCatalog(path, sources)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, ok := reopened.Lookup("vendor/vision")
	if !ok || !reflect.DeepEqual(got, unchanged) || requests.Load() != before {
		t.Fatalf("offline startup did not restore the complete cached card: %+v", got)
	}
	var next int64
	if err := reopened.db.QueryRow(`SELECT next_refresh FROM sources WHERE id='openrouter'`).Scan(&next); err != nil || time.Unix(next, 0).Before(time.Now()) {
		t.Fatal("refresh cadence did not survive restart")
	}
}

func TestCatalogFailedRefreshPreservesLastGoodDatabaseAndSnapshot(t *testing.T) {
	var body atomic.Value
	body.Store(catalogRouterFixture)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body.Load().(string)) }))
	defer server.Close()
	source := ModelSource{"openrouter", server.URL, "openrouter"}
	c, err := OpenModelCatalog(filepath.Join(t.TempDir(), "catalog.db"), []ModelSource{source})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.refresh(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	before, _ := c.Lookup("vendor/vision")
	for _, invalid := range []string{`{"data":[`, strings.Repeat(" ", modelCatalogMaxBytes+1)} {
		body.Store(invalid)
		if err := c.refresh(context.Background(), source); err == nil {
			t.Fatal("bad source response was accepted")
		}
		if got, _ := c.Lookup("vendor/vision"); !reflect.DeepEqual(got, before) {
			t.Fatal("bad response replaced the last good snapshot")
		}
	}
	if _, err := c.db.Exec(`CREATE TRIGGER reject_model BEFORE INSERT ON models WHEN NEW.model='bad' BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	body.Store(`{"data":[{"id":"bad"}]}`)
	if err := c.refresh(context.Background(), source); err == nil {
		t.Fatal("injected transaction failure was ignored")
	}
	var cached []byte
	if err := c.db.QueryRow(`SELECT body FROM sources WHERE id='openrouter'`).Scan(&cached); err != nil || string(cached) != catalogRouterFixture {
		t.Fatal("failed transaction overwrote the source payload")
	}
	if err := c.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Lookup("vendor/vision"); !reflect.DeepEqual(got, before) {
		t.Fatal("failed transaction changed persisted model records")
	}
}

func TestCatalogCloseCancelsSilentBackgroundDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	c, err := OpenModelCatalog(filepath.Join(t.TempDir(), "catalog.db"), []ModelSource{{"openrouter", server.URL, "openrouter"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Start()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("background refresh never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel and join the catalog download")
	}
	select {
	case <-c.Updates():
		t.Fatal("failed/cancelled download emitted a UI metadata update")
	default:
	}
}

func TestModelCatalogPublicSources(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_CATALOG") != "1" {
		t.Skip("explicit live public-catalog acceptance")
	}
	sources := DefaultModelSources()
	c, err := OpenModelCatalog(filepath.Join(t.TempDir(), "catalog.db"), sources)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, source := range sources {
		if err := c.refresh(ctx, source); err != nil {
			t.Fatalf("%s: %v", source.ID, err)
		}
		var count int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM models WHERE source=?`, source.ID).Scan(&count); err != nil || count == 0 {
			t.Fatalf("%s: no persisted public models (%v)", source.ID, err)
		}
		t.Logf("%s: %d public models cached", source.ID, count)
	}
}
