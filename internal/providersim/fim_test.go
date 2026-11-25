package providersim

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

var fimCases = []struct {
	wire                   Wire
	path, operation, model string
}{
	{FIMOpenAI, "/v1/completions", "/completions", "gpt-3.5-turbo-instruct"},
	{FIMDeepSeek, "/beta/completions", "/completions", "deepseek-flash"},
	{FIMCodestral, "/v1/fim/completions", "/fim/completions", "codestral-latest"},
}

func sendFIM(t *testing.T, url string, obj map[string]any) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), "POST", url, bytes.NewReader(jsonBytes(obj)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return do(t, r)
}

func decodeFIMBody(t *testing.T, wire Wire, raw []byte, root string) map[string]any {
	t.Helper()
	if wire == FIMOpenAI {
		return googleChecked(t, wire, root, raw)
	}
	v, err := decodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

func readFIMStream(t *testing.T, wire Wire, res *http.Response, wantDone bool) []map[string]any {
	t.Helper()
	raw := readResponse(t, res)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected stream, got %s: %s", res.Status, raw)
	}
	if !bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Fatal("incomplete SSE frame")
	}
	frames := strings.Split(strings.TrimSuffix(string(raw), "\n\n"), "\n\n")
	done := frames[len(frames)-1] == "data: [DONE]"
	if done != wantDone {
		t.Fatalf("unexpected termination: %q", frames)
	}
	if done {
		frames = frames[:len(frames)-1]
	}
	var rows []map[string]any
	for _, frame := range frames {
		data, ok := strings.CutPrefix(frame, "data: ")
		if !ok {
			t.Fatalf("invalid data-only SSE: %q", frame)
		}
		root := "CompletionStreamChunk"
		if !wantDone && len(rows) == len(frames)-1 {
			root = "ErrorResponse"
		}
		rows = append(rows, decodeFIMBody(t, wire, []byte(data), root))
	}
	return rows
}

func TestFIMCompletions(t *testing.T) {
	for _, tc := range fimCases {
		t.Run(string(tc.wire), func(t *testing.T) {
			s := New(t)
			url := s.BaseURL(tc.wire) + tc.operation
			if bad := get(t, url); bad.StatusCode != http.StatusMethodNotAllowed {
				t.Fatal("completion accepted GET:", bad.Status)
			}
			const prompt, suffix = " \r\nπ(", ")終\n"
			obj := map[string]any{"model": tc.model, "prompt": prompt, "suffix": suffix, "max_tokens": 14}
			minLimit := ""
			if tc.wire == FIMCodestral {
				obj["min_tokens"], minLimit = 1, "1"
			}
			turn := Turn{}.WithTextDeltas("α", " β").WithUsage(Usage{Input: 18, Output: 4, Cached: 2}).WithFinish(FinishLength)
			for _, mode := range []string{"json", "stream", "stream_with_usage"} {
				stream := mode != "json"
				includeUsage := mode == "stream_with_usage" && tc.wire != FIMCodestral
				obj["stream"] = stream
				if includeUsage {
					obj["stream_options"] = map[string]bool{"include_usage": true}
				}
				s.Script(turn)
				res := sendFIM(t, url, obj)
				req, _ := s.Last()
				if req.Wire != tc.wire || req.Path != tc.path || req.Method != "POST" || req.Stream != stream ||
					req.Prompt != prompt || req.Suffix != suffix || req.OutputLimit != "14" || req.MinOutputLimit.String() != minLimit || req.Model != tc.model {
					t.Fatalf("wrong journal entry: %+v", req)
				}
				var rows []map[string]any
				if stream {
					rows = readFIMStream(t, tc.wire, res, true)
				} else {
					if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" {
						t.Fatal(res.Status, string(readResponse(t, res)))
					}
					rows = []map[string]any{decodeFIMBody(t, tc.wire, readResponse(t, res), "CreateCompletionResponse")}
				}
				usageOnly := tc.wire == FIMOpenAI && includeUsage
				expected := 1
				if stream {
					expected = 3
					if usageOnly {
						expected++
					}
				}
				if len(rows) != expected {
					t.Fatalf("%s: wrong event count %d: %v", mode, len(rows), rows)
				}
				for i, row := range rows {
					usage, reported := row["usage"]
					wantUsage := i == len(rows)-1 && (!stream || tc.wire != FIMOpenAI || includeUsage)
					if (usage != nil) != wantUsage || (reported != (wantUsage || includeUsage)) {
						t.Fatalf("%s frame %d: wrong usage presence: %v", mode, i, row)
					}
					if wantUsage {
						u := usage.(map[string]any)
						if numField(t, u, "prompt_tokens") != 18 || numField(t, u, "completion_tokens") != 4 || numField(t, u, "total_tokens") != 22 {
							t.Fatal("wrong usage:", u)
						}
						if tc.wire == FIMDeepSeek && (numField(t, u, "prompt_cache_hit_tokens") != 2 || numField(t, u, "prompt_cache_miss_tokens") != 16) {
							t.Fatal("wrong DeepSeek cache usage:", u)
						}
					}
					if row["id"] == "" || row["id"] == nil || row["id"] != rows[0]["id"] || row["model"] != tc.model || row["created"] == nil {
						t.Fatal("wrong completion identity:", row)
					}
					if usageOnly && i == len(rows)-1 {
						if len(row["choices"].([]any)) != 0 {
							t.Fatal("OpenAI final usage chunk has choices")
						}
						continue
					}
					c := choice(row)
					val := c["text"]
					if tc.wire == FIMCodestral {
						key := "message"
						if stream {
							key = "delta"
						}
						val = googleObjectAt(c, key)["content"]
						if c["text"] != nil {
							t.Fatal("Codestral emitted a legacy text choice")
						}
					}
					wantText := "α β"
					var wantFinish any = "length"
					if stream {
						wantText = []string{"α", " β", ""}[i]
						if i < 2 {
							wantFinish = nil
						}
					}
					if val != wantText || c["finish_reason"] != wantFinish || numField(t, c, "index") != 0 {
						t.Fatal("wrong completion choice:", c)
					}
				}
			}
		})
	}
}

func TestFIMFaults(t *testing.T) {
	for _, tc := range fimCases {
		t.Run(string(tc.wire), func(t *testing.T) {
			s := New(t)
			obj := map[string]any{"model": tc.model, "prompt": "a", "stream": true}
			url := s.BaseURL(tc.wire) + tc.operation
			for kind, status := range map[FaultKind]int{Auth: 401, ContextOverflow: 400, InvalidRequest: 400, ModelNotFound: 404, Quota: 429, RateLimit: 429, Overload: 503, BadGateway: 502, RequestTimeout: 408} {
				const text = "scripted failure"
				s.Script(HTTPError(kind).WithFaultMessage(text).WithRetryHint(RetryAfterSeconds(7)))
				res := sendFIM(t, url, obj)
				if kind == Quota && tc.wire != FIMOpenAI {
					status = 402
				} else if kind == ModelNotFound && tc.wire == FIMCodestral {
					status = 400
				}
				if res.StatusCode != status || res.Header.Get("Retry-After") != "7" {
					t.Fatal(kind, res.Status, res.Header)
				}
				body := decodeFIMBody(t, tc.wire, readResponse(t, res), "ErrorResponse")
				msg := googleObjectAt(body, "error")["message"]
				if tc.wire == FIMCodestral {
					msg = body["message"]
					if kind == Auth {
						msg = body["detail"]
					} else if body["object"] != "error" || numField(t, body, "raw_status_code") != status {
						t.Fatal("invalid Mistral error:", body)
					}
				}
				if msg != text {
					t.Fatal(kind, body)
				}
			}
			s.Script(Text("partial").WithStreamError(1, Overload), Text("partial").WithCut(1))
			rows := readFIMStream(t, tc.wire, sendFIM(t, url, obj), tc.wire != FIMOpenAI)
			if len(rows) != 2 {
				t.Fatal("wrong fault position:", rows)
			}
			if tc.wire == FIMOpenAI {
				if googleObjectAt(rows[1], "error")["code"] != "server_is_overloaded" {
					t.Fatal(rows)
				}
			} else {
				want := map[Wire]string{FIMDeepSeek: "insufficient_system_resource", FIMCodestral: "error"}[tc.wire]
				if choice(rows[1])["finish_reason"] != want {
					t.Fatal(rows)
				}
			}
			res := sendFIM(t, url, obj)
			b, err := io.ReadAll(res.Body)
			if !errors.Is(err, io.ErrUnexpectedEOF) || !bytes.Contains(b, []byte(`"partial"`)) || bytes.Contains(b, []byte("[DONE]")) {
				t.Fatal("cut was not a transport failure after content:", string(b), err)
			}
		})
	}
}

func TestFIMRejectsWrongWireAndScript(t *testing.T) {
	for _, tc := range fimCases {
		t.Run(string(tc.wire), func(t *testing.T) {
			reports := make(chan string, 1)
			s := newServer(t, func(msg string, args ...any) { reports <- fmt.Sprintf(msg, args...) }, nil)
			url := s.BaseURL(tc.wire) + tc.operation
			s.Script(CallTools(ToolCall{Name: "not-FIM"}), Text("ok"))
			bad := sendFIM(t, url, map[string]any{"model": tc.model, "messages": []any{"hi"}})
			rejected, _ := s.Last()
			if bad.StatusCode != 400 || rejected.Violation == "" || !strings.Contains(<-reports, rejected.Violation) {
				t.Fatal("accepted chat request on a FIM route")
			}
			obj := map[string]any{"model": tc.model, "prompt": "hi"}
			res := sendFIM(t, url, obj)
			if res.StatusCode != 500 || !strings.Contains(<-reports, "use a text turn") {
				t.Fatal("unsupported turn did not report misuse")
			}
			ok := sendFIM(t, url, obj)
			if ok.StatusCode != 200 || !bytes.Contains(readResponse(t, ok), []byte(`"ok"`)) {
				t.Fatal("invalid request consumed the wrong scripted turn")
			}
		})
	}
}

func TestFIMUsageAndNativeStreamFaultLimits(t *testing.T) {
	for _, tc := range fimCases {
		t.Run(string(tc.wire), func(t *testing.T) {
			reports := make(chan string, 1)
			s := newServer(t, func(msg string, args ...any) { reports <- fmt.Sprintf(msg, args...) }, nil)
			url := s.BaseURL(tc.wire) + tc.operation
			obj := map[string]any{"model": tc.model, "prompt": ""}
			for _, stream := range []bool{false, true} {
				obj["stream"] = stream
				s.Script(Text("ok").WithoutUsage())
				res := sendFIM(t, url, obj)
				if tc.wire == FIMDeepSeek && stream || tc.wire == FIMCodestral && !stream {
					assertFIMMisuse(t, reports, res, "requires usage")
				} else if res.StatusCode != 200 || bytes.Contains(readResponse(t, res), []byte(`"usage"`)) {
					t.Fatal("optional usage was not omitted")
				}
			}
			if tc.wire == FIMOpenAI {
				return
			}
			for _, tr := range []Turn{StreamError(0, Auth), StreamError(0, Overload).WithFaultMessage("unsupported")} {
				s.Script(tr)
				assertFIMMisuse(t, reports, sendFIM(t, url, obj), "use HTTPError")
			}
			s.Script(Text("complete").WithStreamError(2, Overload))
			assertFIMMisuse(t, reports, sendFIM(t, url, obj), "before the finish")
		})
	}
}

func assertFIMMisuse(t *testing.T, reports <-chan string, res *http.Response, want string) {
	t.Helper()
	if res.StatusCode != 500 {
		t.Fatal("invalid FIM script accepted:", string(readResponse(t, res)))
	}
	select {
	case msg := <-reports:
		if !strings.Contains(msg, want) {
			t.Fatal(msg)
		}
	default:
		t.Fatal("invalid script did not report misuse")
	}
}
