package main

import "testing"

func TestVisibleDropsThinkBlocks(t *testing.T) {
	cases := map[string]string{
		`<think>plan</think>Hello`: `Hello`,
		`Hello<think>more`:         `Hello`,
		`no think`:                 `no think`,
		// Qwen3 and R1 templates put the opening tag in the prompt.
		`reasoning only</think>Hello`: `Hello`,
		`a</think>b</think>c`:         `c`,
	}
	for in, want := range cases {
		if got := visible(in); got != want {
			t.Errorf("visible(%q) = %q, want %q", in, got, want)
		}
	}
}
