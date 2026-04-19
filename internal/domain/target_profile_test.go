package domain

import "testing"

func TestNormalizeOskellyTargetProfileURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     TargetProfileDescriptor
		want      string
		wantError bool
	}{
		{
			name:      "valid profile url",
			input:     TargetProfileDescriptor("https://oskelly.ru/profile/1001"),
			want:      "https://oskelly.ru/profile/1001",
			wantError: false,
		},
		{
			name:      "empty value",
			input:     TargetProfileDescriptor("   "),
			wantError: true,
		},
		{
			name:      "invalid format",
			input:     TargetProfileDescriptor("https://oskelly.ru/profiles/1001"),
			wantError: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeOskellyTargetProfileURL(tc.input)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got none (value=%q)", got)
				}
				if !IsDomainErrorCode(err, ErrorCodeFollowTargetProfile) {
					t.Fatalf("expected error code %s, got %v", ErrorCodeFollowTargetProfile, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
