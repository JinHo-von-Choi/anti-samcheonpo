package dashboard

import "net/http"

// reviewQueue omits sampling strata and other reviewers' answers so they do
// not influence a new independent review. The queue itself is never a label.
func (s *Server) reviewQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET 요청 필요", 405)
		return
	}
	type item struct {
		Session         string `json:"session"`
		Start           int64  `json:"start"`
		End             int64  `json:"end"`
		NativeAvailable bool   `json:"native_source_available"`
	}
	var items = []item{}
	digest := ""
	if s.queue != nil {
		digest = s.queue.Digest
		for _, x := range s.queue.Items {
			items = append(items, item{x.Session, x.Start, x.End, x.NativeSourceAvailable})
		}
	}
	writeJSON(w, map[string]any{"digest": digest, "items": items, "labels_created": false, "independence": "requires_distinct_humans_not_just_ids"})
}
