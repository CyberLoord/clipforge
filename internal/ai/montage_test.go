package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestMontageRequest(t *testing.T) {
	frames := montageFrames(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model    string `json:"model"`
			Tokens   int    `json:"max_completion_tokens"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Format struct {
				Type   string `json:"type"`
				Schema struct {
					Name   string         `json:"name"`
					Strict bool           `json:"strict"`
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" || payload.Model != "vision-model" || payload.Tokens < 8192 {
			t.Errorf("unexpected request: %s model=%s tokens=%d", r.URL.Path, payload.Model, payload.Tokens)
		}
		if payload.Format.Type != "json_schema" || payload.Format.Schema.Name != "montages" || !payload.Format.Schema.Strict {
			t.Errorf("unexpected format: %+v", payload.Format)
		}
		validateStrictSchema(t, payload.Format.Schema.Schema)
		if len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[1].Role != "user" {
			t.Errorf("unexpected messages: %+v", payload.Messages)
			return
		}
		var parts []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Image struct {
				URL    string `json:"url"`
				Detail string `json:"detail"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(payload.Messages[1].Content, &parts); err != nil {
			t.Error(err)
			return
		}
		if len(parts) != 1+2*len(frames) || !strings.Contains(parts[0].Text, "17.642667") {
			t.Errorf("unexpected frame content: %+v", parts)
			return
		}
		for i, frame := range frames {
			label, visual := parts[1+i*2], parts[2+i*2]
			if label.Type != "text" || !strings.HasPrefix(label.Text, "Frame at ") || visual.Type != "image_url" || visual.Image.Detail != "low" {
				t.Errorf("invalid content pair: %+v %+v", label, visual)
			}
			encoded, ok := strings.CutPrefix(visual.Image.URL, "data:image/jpeg;base64,")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if !ok || err != nil || !bytes.Equal(decoded, frame.JPEG) {
				t.Errorf("image %d changed", i)
			}
		}
		_, _ = io.WriteString(w, selectionResponse(montageSelection()))
	}))
	defer server.Close()
	clips, err := NewOpenAI("test-key", server.URL, "vision-model").SelectMontages(context.Background(), frames, 17.642667)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 2 || clips[0].Title != "Поворот" || clips[0].Style != "dynamic" || clips[0].Start != 0 || clips[0].End != 0 || !reflect.DeepEqual(clips[0].Cuts, []domain.Cut{{Start: 1, End: 4}, {Start: 9, End: 12}}) {
		t.Fatalf("invalid montages: %+v", clips)
	}
}

func TestMontageRejectsMalformedSelection(t *testing.T) {
	valid := montageSelection()
	cases := map[string]string{
		"malformed envelope": `not json`,
		"no choices":         `{"choices":[]}`,
		"truncated":          `{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`,
		"refusal":            `{"choices":[{"finish_reason":"stop","message":{"refusal":"sensitive refusal text","content":"{}"}}]}`,
		"multiple choices":   `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}},{"finish_reason":"stop","message":{"content":"{}"}}]}`,
		"malformed content":  selectionResponse(`not json`),
		"missing clips":      selectionResponse(`{}`),
		"null content":       selectionResponse(`null`),
		"trailing content":   selectionResponse(valid + `{}`),
		"unknown field":      selectionResponse(strings.Replace(valid, `"style":"dynamic"`, `"style":"dynamic","unknown":1`, 1)),
		"missing start":      selectionResponse(strings.Replace(valid, `"start":1,`, ``, 1)),
		"null timestamp":     selectionResponse(strings.Replace(valid, `"start":1`, `"start":null`, 1)),
		"invalid style":      selectionResponse(strings.Replace(valid, `"style":"dynamic"`, `"style":"random"`, 1)),
		"duplicate style":    selectionResponse(strings.Replace(valid, `"style":"cinematic"`, `"style":"dynamic"`, 1)),
		"duplicate plan":     selectionResponse(strings.Replace(valid, `[{"start":2,"end":7},{"start":12,"end":16}]`, `[{"start":1,"end":4},{"start":9,"end":12}]`, 1)),
		"short cut":          selectionResponse(strings.Replace(valid, `"end":4`, `"end":1.5`, 1)),
		"outside source":     selectionResponse(strings.Replace(valid, `"end":16`, `"end":18`, 1)),
		"overlapping cuts":   selectionResponse(strings.Replace(valid, `"start":9`, `"start":3`, 1)),
		"too much source":    selectionResponse(strings.Replace(valid, `[{"start":2,"end":7},{"start":12,"end":16}]`, `[{"start":0,"end":10},{"start":10,"end":17}]`, 1)),
		"oversized content":  selectionResponse(strings.Repeat(" ", maxMontageContentBytes+1) + valid),
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := responseServer(response, http.StatusOK)
			defer server.Close()
			_, err := NewOpenAI("test", server.URL, "test").SelectMontages(context.Background(), montageFrames(t), 17.642667)
			if err == nil || IsTemporary(err) || strings.Contains(err.Error(), "sensitive refusal text") {
				t.Fatalf("expected safe permanent validation error, got %v", err)
			}
		})
	}
}

func TestMontagePreservesCutOrderInThreeVariants(t *testing.T) {
	selection := `{"clips":[{"title":"Динамика","reason":"Сначала действие, затем общий план","style":"dynamic","cuts":[{"start":9,"end":12},{"start":1,"end":4}]},{"title":"Пейзаж","reason":"Два продолжительных плана","style":"cinematic","cuts":[{"start":0,"end":4},{"start":10,"end":14}]},{"title":"Развитие","reason":"Последовательность планов","style":"story","cuts":[{"start":2,"end":5},{"start":7,"end":10}]}]}`
	server := responseServer(selectionResponse(selection), http.StatusOK)
	defer server.Close()
	frames := montageFrames(t)
	frames[2].Time = 14
	clips, err := NewOpenAI("test", server.URL, "test").SelectMontages(context.Background(), frames, 15)
	if err != nil || len(clips) != 3 {
		t.Fatalf("clips=%v error=%v", clips, err)
	}
	if clips[0].Cuts[0].Start != 9 || clips[0].Cuts[1].Start != 1 {
		t.Fatalf("cut order changed: %+v", clips[0].Cuts)
	}
}

func TestMontageRejectsInvalidFramesBeforeRequest(t *testing.T) {
	frames := montageFrames(t)
	tooMany := make([]domain.Frame, maxMontageFrames+1)
	for i := range tooMany {
		tooMany[i] = domain.Frame{Time: float64(i) / 10, JPEG: frames[0].JPEG}
	}
	cases := map[string]struct {
		frames   []domain.Frame
		duration float64
	}{
		"missing frames":  {nil, 20},
		"single frame":    {frames[:1], 20},
		"too many frames": {tooMany, 20},
		"short source":    {frames, 14.999},
		"infinite source": {frames, math.Inf(1)},
		"nan source":      {frames, math.NaN()},
		"duplicate time":  {[]domain.Frame{frames[0], frames[0]}, 20},
		"unordered time":  {[]domain.Frame{frames[1], frames[0]}, 20},
		"negative time":   {[]domain.Frame{{Time: -1, JPEG: frames[0].JPEG}, frames[1]}, 20},
		"outside time":    {[]domain.Frame{frames[0], {Time: 21, JPEG: frames[1].JPEG}}, 20},
		"nan time":        {[]domain.Frame{frames[0], {Time: math.NaN(), JPEG: frames[1].JPEG}}, 20},
		"infinite time":   {[]domain.Frame{frames[0], {Time: math.Inf(1), JPEG: frames[1].JPEG}}, 20},
		"empty jpeg":      {[]domain.Frame{frames[0], {Time: 5}}, 20},
		"malformed jpeg":  {[]domain.Frame{frames[0], {Time: 5, JPEG: []byte("not jpeg")}}, 20},
		"oversized jpeg":  {[]domain.Frame{frames[0], {Time: 5, JPEG: make([]byte, maxFrameBytes+1)}}, 20},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid frames reached OpenAI")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	provider := NewOpenAI("test", server.URL, "test")
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.SelectMontages(context.Background(), tc.frames, tc.duration); err == nil || IsTemporary(err) {
				t.Fatalf("expected permanent frame validation error, got %v", err)
			}
		})
	}
}

func TestMontageAPIErrorAndCancellation(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := responseServer("secret provider response", status)
			defer server.Close()
			_, err := NewOpenAI("test", server.URL, "test").SelectMontages(context.Background(), montageFrames(t), 20)
			if err == nil || strings.Contains(err.Error(), "secret provider response") || IsTemporary(err) != (status == 429 || status >= 500) {
				t.Fatalf("incorrect API error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewOpenAI("test", "https://example.invalid/v1", "test").SelectMontages(ctx, montageFrames(t), 20)
	if !errors.Is(err, context.Canceled) || IsTemporary(err) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestMontageFrameByteLimit(t *testing.T) {
	frames := montageFrames(t)
	data := append(bytes.Clone(frames[0].JPEG), make([]byte, maxFrameBytes-len(frames[0].JPEG))...)
	bounded := make([]domain.Frame, maxMontageFrames)
	for i := range bounded {
		bounded[i] = domain.Frame{Time: float64(i), JPEG: data}
	}
	if err := validateFrames(bounded, 60); err != nil {
		t.Fatalf("rejected frames at byte limit: %v", err)
	}
}

func montageSelection() string {
	return `{"clips":[{"title":" Поворот ","reason":" Короткие смены ракурса ","style":"dynamic","cuts":[{"start":1,"end":4},{"start":9,"end":12}]},{"title":"Общий план","reason":"Длинные планы дают рассмотреть пейзаж","style":"cinematic","cuts":[{"start":2,"end":7},{"start":12,"end":16}]}]}`
}

func montageFrames(t *testing.T) []domain.Frame {
	t.Helper()
	var frames []domain.Frame
	for i, at := range []float64{0, 8, 17} {
		picture := image.NewRGBA(image.Rect(0, 0, 4, 4))
		picture.Set(1, 1, color.RGBA{R: uint8(50 * i), G: 150, B: 100, A: 255})
		var data bytes.Buffer
		if err := jpeg.Encode(&data, picture, nil); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, domain.Frame{Time: at, JPEG: data.Bytes()})
	}
	return frames
}

func validateStrictSchema(t *testing.T, schema map[string]any) {
	t.Helper()
	if schema["type"] == "object" {
		if schema["additionalProperties"] != false {
			t.Error("schema permits extra properties")
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatal("missing schema properties")
		}
		required, ok := schema["required"].([]any)
		if !ok || len(required) != len(properties) {
			t.Fatal("schema has optional properties")
		}
		for _, property := range properties {
			validateStrictSchema(t, property.(map[string]any))
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		validateStrictSchema(t, items)
	}
}
