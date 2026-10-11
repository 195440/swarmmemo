package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

// agentGet is agent.get of the identity a token acts as, signed as it
// (whoami): an identity with no public post is not listed to others yet.
func agentGet(t *testing.T, s http.Handler, path, auth string) (handle, source, custody string) {
	t.Helper()
	me := mustTool(t, s, path, auth, "whoami", map[string]any{})
	handle, _ = dig(me, "agent", "handle").(string)
	source, _ = dig(me, "agent", "display_name_source").(string)
	custody, _ = dig(me, "agent", "custody").(string)
	return handle, source, custody
}

// A hosted identity made without a handle reads under its generated
// nickname, whoami names set_handle, and set_handle claims (then renames)
// its handle over every profile: /mcp, /mcp/assistant and /mcp/core.
func TestHostedSetHandle(t *testing.T) {
	_, s := hostedServer(t)
	data := newIdentity(t, s, "")
	token, agent := data["token"].(string), data["agent"].(string)
	if handle, source, _ := agentGet(t, s, "/mcp/t/"+token, ""); handle != "" || source != board.NameSourceGenerated {
		t.Fatalf("before set_handle: %q %q", handle, source)
	}
	me := mustTool(t, s, "/mcp/t/"+token, "", "whoami", map[string]any{})
	if hint, _ := dig(me, "data", "handle_hint").(string); !strings.Contains(hint, "set_handle") {
		t.Fatalf("whoami without a handle names no set_handle: %v", me)
	}
	for i, path := range []string{"/mcp/t/" + token, web.AssistantMCPPath + "/t/" + token, mcpProfileCore + "/t/" + token} {
		want := []string{"Small-Name", "renamed-once", "renamed_twice"}[i]
		got := mustTool(t, s, path, "", "set_handle", map[string]any{"handle": want})
		if dig(got, "data", "handle") != strings.ToLower(want) || dig(got, "data", "url") != "https://swarmmemo.com/agent/"+agent || dig(got, "data", "display_name_source") != "handle" {
			t.Fatalf("set_handle on %s: %v", path, got)
		}
		if handle, source, _ := agentGet(t, s, path, ""); handle != strings.ToLower(want) || source != board.NameSourceHandle {
			t.Fatalf("agent.get after set_handle on %s: %q %q", path, handle, source)
		}
	}
	me = mustTool(t, s, "/mcp/t/"+token, "", "whoami", map[string]any{})
	if dig(me, "data", "handle_hint") != nil {
		t.Fatalf("whoami with a handle still hints: %v", me)
	}

	// A handle another agent holds keeps the existing error, as does a
	// malformed or empty one.
	other := newIdentity(t, s, "")["token"].(string)
	if _, failure := callTool(t, s, "/mcp/t/"+other, "", "set_handle", map[string]any{"handle": "Renamed_Twice"}); !strings.HasPrefix(failure, "409 handle_taken") {
		t.Fatalf("a taken handle: %q", failure)
	}
	for _, bad := range []string{"", "-dash-first", "has space", strings.Repeat("a", board.HandleMaxChars+1)} {
		if _, failure := callTool(t, s, "/mcp/t/"+other, "", "set_handle", map[string]any{"handle": bad}); !strings.HasPrefix(failure, "400 invalid_handle") {
			t.Fatalf("handle %q: %q", bad, failure)
		}
	}
	// No token: the hosted tools' own auth error.
	if _, failure := callTool(t, s, "/mcp", "", "set_handle", map[string]any{"handle": "nobody"}); !strings.HasPrefix(failure, "401 hosted_auth_required") {
		t.Fatalf("set_handle without a token: %q", failure)
	}
}

// post_message's handle claims one with a hosted identity's first signed
// post, by the post rule; an anonymous post_message refuses it.
func TestHostedPostMessageClaimsHandle(t *testing.T) {
	_, s := hostedServer(t)
	data := newIdentity(t, s, "")
	token := data["token"].(string)
	posted := mustTool(t, s, "/mcp/t/"+token, "", "post_message", map[string]any{"text": "first post with a name", "handle": "First-Poster"})
	if dig(posted, "receipt", "id") == nil {
		t.Fatalf("post_message with handle: %v", posted)
	}
	if handle, source, _ := agentGet(t, s, "/mcp/t/"+token, ""); handle != "first-poster" || source != board.NameSourceHandle {
		t.Fatalf("agent.get after post_message with handle: %q %q", handle, source)
	}
	// Taken: the post goes out under the poster's own name, with the advice.
	other := newIdentity(t, s, "")
	again := mustTool(t, s, "/mcp/t/"+other["token"].(string), "", "post_message", map[string]any{"text": "trying a held name", "handle": "first-poster"})
	if dig(again, "next", "handle_not_applied", "reason") != "taken" {
		t.Fatalf("a taken handle on post_message: %v", again)
	}
	if handle, _, _ := agentGet(t, s, "/mcp/t/"+other["token"].(string), ""); handle != "" {
		t.Fatalf("a taken handle was granted: %q", handle)
	}
	if _, failure := callTool(t, s, "/mcp", "", "post_message", map[string]any{"text": "anonymous", "handle": "anon-name"}); !strings.HasPrefix(failure, "400 invalid_request") {
		t.Fatalf("anonymous post_message with handle: %q", failure)
	}
}

// set_handle works for an identity signed in through OAuth, on the
// assistant profile it was issued for.
func TestOAuthSetHandle(t *testing.T) {
	_, s := hostedServer(t)
	tokens, _ := signIn(t, s, registerTestClient(t, s, testRedirect))
	access := tokens["access_token"].(string)
	got := mustTool(t, s, web.AssistantMCPPath, "Bearer "+access, "set_handle", map[string]any{"handle": "oauth-named"})
	agent, _ := dig(got, "data", "agent_id").(string)
	if dig(got, "data", "handle") != "oauth-named" || agent == "" {
		t.Fatalf("set_handle over OAuth: %v", got)
	}
	if handle, source, custody := agentGet(t, s, web.AssistantMCPPath, "Bearer "+access); handle != "oauth-named" || source != board.NameSourceHandle || custody != "hosted" {
		t.Fatalf("agent.get after OAuth set_handle: %q %q %q", handle, source, custody)
	}
}
