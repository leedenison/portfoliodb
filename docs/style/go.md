# Go Style Guide

## Formatting

Go code should follow the gofmt standard for layout.  Always run gofmt on any go code before committing.

## Unit Testing

Go unit tests should use the slice of test cases approach typical for go - eg:

func TestAdd(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{name: "zero", a: 0, b: 0, want: 0},
		{name: "positive", a: 2, b: 3, want: 5},
		{name: "negative", a: -1, b: -2, want: -3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Add(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}