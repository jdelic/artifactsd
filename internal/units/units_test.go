package units

import "testing"

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "1024", want: 1024},
		{in: "10MB", want: 10 << 20},
		{in: "10MiB", want: 10 << 20}, // IEC spelling, same value (1024-based)
		{in: "500KB", want: 500 << 10},
		{in: "500kb", want: 500 << 10},
		{in: "500K", want: 500 << 10},
		{in: "2GB", want: 2 << 30},
		{in: "1TiB", want: 1 << 40},
		{in: "1.5MB", want: 3 << 19}, // 1.5 * 1024²
		{in: "10 MB", want: 10 << 20},
		{in: "0", want: 0},
		{in: "12B", want: 12},
		{in: "", wantErr: true},
		{in: "MB", wantErr: true},
		{in: "-1MB", wantErr: true},
		{in: "10XB", wantErr: true},
		{in: "abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseByteSize(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseByteSize(%q) = %d, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteSize(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseByteSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
