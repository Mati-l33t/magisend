package server

import (
	"encoding/json"
	"testing"
)

func TestShareLimitsAcrossLinks(t *testing.T) {
	for _, scenario := range []string{"selection", "live_create", "edit", "move", "upload", "recipient_add"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			setupAdmin(t, a)
			token := login(t, a, "mati")
			p, e := a.policy()
			if e != nil {
				t.Fatal(e)
			}
			p.MaxFile = 5
			p.MaxShare = 5
			raw, _ := json.Marshal(p)
			folderCall(t, a, token, "PUT", "/api/admin/policy", string(raw), 200)
			folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true,"grant_edit":true,"add_shared":true}`, 200)
			fid := folderCall(t, a, token, "POST", "/api/folders", `{"name":"limited"}`, 201)["id"].(string)
			first := libraryUpload(t, a, token, fid, "first.txt", "abc").Files[0].ID
			switch scenario {
			case "selection":
				second := libraryUpload(t, a, token, "", "second.txt", "def").Files[0].ID
				w := call(a, "POST", "/api/library/shares", `{"files":["`+first+`","`+second+`"]}`, token)
				if w.Code != 413 {
					t.Fatalf("selection bypass: want413 got%d %s", w.Code, w.Body)
				}
			case "live_create":
				libraryUpload(t, a, token, fid, "second.txt", "def")
				w := call(a, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, token)
				if w.Code != 413 {
					t.Fatalf("live create bypass: want413 got%d", w.Code)
				}
			case "edit":
				second := libraryUpload(t, a, token, "", "second.txt", "x").Files[0].ID
				folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+first+`","`+second+`"]}`, 201)
				w := call(a, "PUT", "/api/library/files/"+first+"/text", textBody(1, "abcde"), token)
				if w.Code != 413 {
					t.Fatalf("edit bypass: want413 got%d", w.Code)
				}
			default:
				sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
				if scenario == "move" {
					second := libraryUpload(t, a, token, "", "second.txt", "def").Files[0].ID
					w := call(a, "PATCH", "/api/library/files/"+second, `{"folder_id":"`+fid+`"}`, token)
					if w.Code != 413 {
						t.Fatalf("move bypass: want413 got%d", w.Code)
					}
				} else if scenario == "upload" {
					w := call(a, "POST", "/api/library/uploads", `{"folder_id":"`+fid+`","files":[{"name":"second.txt","size":3}]}`, token)
					if w.Code != 413 {
						t.Fatalf("upload bypass: want413 got%d", w.Code)
					}
				} else {
					// Existing collaborator path already enforces this limit; keep it covered.
					folderCall(t, a, token, "POST", "/api/public/"+sid+"/files?name=second.txt", "def", 413)
				}
			}
		})
	}
}
