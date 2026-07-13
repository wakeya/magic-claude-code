package failover

import (
	"testing"

	"magic-claude-code/internal/config"
)

// sameModelProviders 构造指定顺序的 provider 列表；sameModel 的几个映射到 glm-5.2，
// fallback 映射到其它模型。
func sameModelProviders(order []string, sameModel []string) []config.Provider {
	same := map[string]bool{}
	for _, id := range sameModel {
		same[id] = true
	}
	out := make([]config.Provider, 0, len(order))
	for _, id := range order {
		p := config.Provider{ID: id, Name: id, APIURL: "https://" + id, APIFormat: config.APIFormatAnthropic, Enabled: true}
		if same[id] {
			p.ModelMappings = map[string]string{"claude-opus-4-8": "glm-5.2"}
		} else {
			p.ModelMappings = map[string]string{"claude-opus-4-8": "kimi-" + id}
		}
		out = append(out, p)
	}
	return out
}

// TestSelectCandidatesUsesProviderOrderWithinSameMappedModel：
// 列表顺序 [A, C, B, D]，A 失败，B/C 同映射模型，D fallback。
// 同模型段必须按列表顺序 [C, B]，再 fallback [D] → [C, B, D]。
func TestSelectCandidatesUsesProviderOrderWithinSameMappedModel(t *testing.T) {
	providers := sameModelProviders([]string{"a", "c", "b", "d"}, []string{"b", "c"})
	m, _ := newTestManager(t, providers, "a")

	got := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providers)
	want := []string{"c", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", providerIDs(got), want)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("candidates[%d] = %s, want %s (full: %v)", i, got[i].ID, want[i], providerIDs(got))
		}
	}
}

// TestSelectCandidatesUsesProviderOrderWithinFallbackGroup：
// 全是 fallback（无同模型），列表顺序 [A, C, B, D]，A 失败 → fallback 段按列表顺序 [C, B, D]。
func TestSelectCandidatesUsesProviderOrderWithinFallbackGroup(t *testing.T) {
	// 没有任何候选与 failed 映射相同模型，全部进 fallback 段。
	providers := sameModelProviders([]string{"a", "c", "b", "d"}, []string{"a"}) // 只有 a 同模型，但 a 是失败来源
	m, _ := newTestManager(t, providers, "a")

	got := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providers)
	want := []string{"c", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", providerIDs(got), want)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("fallback[%d] = %s, want %s (full: %v)", i, got[i].ID, want[i], providerIDs(got))
		}
	}
}

// TestSelectCandidatesReflectsReorderedList：
// 同一组 provider，顺序从 [A, C, B, D] 改为 [A, B, C, D]，
// 候选同模型段从 [C, B] 变 [B, C]。
func TestSelectCandidatesReflectsReorderedList(t *testing.T) {
	providers1 := sameModelProviders([]string{"a", "c", "b", "d"}, []string{"b", "c"})
	m, _ := newTestManager(t, providers1, "a")
	got1 := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providers1)

	providers2 := sameModelProviders([]string{"a", "b", "c", "d"}, []string{"b", "c"})
	got2 := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providers2)

	if providerIDs(got1)[0] != "c" || providerIDs(got1)[1] != "b" {
		t.Fatalf("order [a,c,b,d] same-model = %v, want [c b ...]", providerIDs(got1))
	}
	if providerIDs(got2)[0] != "b" || providerIDs(got2)[1] != "c" {
		t.Fatalf("order [a,b,c,d] same-model = %v, want [b c ...]", providerIDs(got2))
	}
}

func providerIDs(ps []config.Provider) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}
