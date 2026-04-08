package worker

import "testing"

func TestNormalizeScreenshotPayloadGeneratesPNGForNonImagePayload(t *testing.T) {
	t.Parallel()

	got, err := normalizeScreenshotPayload([]byte("verify-screenshot:error"))
	if err != nil {
		t.Fatalf("normalizeScreenshotPayload() error = %v", err)
	}
	if len(got) < len(pngSignature) {
		t.Fatalf("expected png payload, got %d bytes", len(got))
	}
	if string(got[:len(pngSignature)]) != string(pngSignature) {
		t.Fatal("expected png signature prefix")
	}
}

func TestNormalizeScreenshotPayloadKeepsPNGPayload(t *testing.T) {
	t.Parallel()

	in := append([]byte{}, pngSignature...)
	in = append(in, []byte("rest")...)

	got, err := normalizeScreenshotPayload(in)
	if err != nil {
		t.Fatalf("normalizeScreenshotPayload() error = %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("expected same length %d, got %d", len(in), len(got))
	}
	if &got[0] == &in[0] {
		t.Fatal("expected cloned buffer, got shared slice")
	}
}
