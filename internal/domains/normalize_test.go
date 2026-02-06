package domains

import "testing"

func TestNormalizeDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty", raw: " ", wantErr: true},
		{name: "basic_trim_lower_dot", raw: " Example.COM. ", want: "example.com"},
		{name: "reject_scheme", raw: "https://example.com", wantErr: true},
		{name: "reject_path", raw: "example.com/path", wantErr: true},
		{name: "reject_port", raw: "example.com:443", wantErr: true},
		{name: "reject_creds", raw: "user@example.com", wantErr: true},
		{name: "reject_wildcard", raw: "*.example.com", wantErr: true},
		{name: "reject_ip", raw: "127.0.0.1", wantErr: true},
		{name: "require_dot", raw: "localhost", wantErr: true},
		{name: "reject_bad_label", raw: "a..example.com", wantErr: true},
		{name: "idna_to_ascii", raw: "bücher.example", want: "xn--bcher-kva.example"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeDomain(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeDomain error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeDomain mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

