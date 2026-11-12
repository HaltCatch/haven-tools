package toolkit

import (
	"strings"
	"testing"
)

func TestNotificationNavigationAllowsOnlyAuthenticatedTopLevelPages(t *testing.T) {
	_, server := testService(t)
	const id = "b6d24bea-e7ec-4ce9-91db-501fddc979d6"
	for _, check := range []struct {
		path, method, mode, dest, token string
		want                            int
	}{
		{"/tools/tasks/" + id, "GET", "navigate", "document", strings.Repeat("a", 32), 200},
		{"/tools/tasks", "GET", "navigate", "document", strings.Repeat("a", 32), 200},
		{"/tools/", "GET", "navigate", "document", strings.Repeat("a", 32), 200},
		{"/tools/tasks/" + id, "GET", "navigate", "document", "", 401},
		{"/tools/tasks/" + id, "GET", "navigate", "iframe", strings.Repeat("a", 32), 403},
		{"/tools/tasks/" + id, "GET", "cors", "empty", strings.Repeat("a", 32), 403},
		{"/tools/v1/catalog", "GET", "navigate", "document", strings.Repeat("a", 32), 403},
		{"/tools/v1/tasks/" + id, "GET", "navigate", "document", strings.Repeat("a", 32), 403},
		{"/tools/tasks/" + id + "/files/video.mp4", "GET", "navigate", "document", strings.Repeat("a", 32), 403},
		{"/tools/v1/execute", "POST", "navigate", "document", strings.Repeat("a", 32), 403},
	} {
		headers := map[string]string{"Origin": "https://ntfy.example", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": check.mode, "Sec-Fetch-Dest": check.dest}
		r := request(t, server.URL+check.path, check.method, "", check.token, headers)
		r.Body.Close()
		if r.StatusCode != check.want {
			t.Fatalf("%s %s %s %s: want %d got %d", check.method, check.path, check.mode, check.dest, check.want, r.StatusCode)
		}
	}
}
