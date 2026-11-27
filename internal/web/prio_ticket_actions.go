package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioIssueRefresher interface {
	RefreshIssue(context.Context, interface {
		FetchIssue(context.Context, string) (jira.Issue, error)
	}, string) (jira.Issue, error)
}

func WithPrioIssueRefresher(refresher prioIssueRefresher) Option {
	return func(s *Server) { s.prioRefresher = refresher }
}

type prioTicketResult struct {
	Message, Change, CreatedHref string
}

type prioTicketResultKey struct{}

var (
	dcaiIssueKey = regexp.MustCompile(`^DCAI-[1-9][0-9]*$`)
	jiraIssueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
)

func (s *Server) prioTicketClient(ctx context.Context) (jira.Client, jira.IssueActions, bool) {
	client, _ := PersonalJiraFromContext(ctx)
	identity, _ := JiraIdentityFromContext(ctx)
	actions, ok := client.(jira.IssueActions)
	return client, actions, ok && identity.AccountID != "" && s.prioRefresher != nil
}

func (s *Server) handlePrioTicket(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Ticket-Anfrage.", http.StatusBadRequest)
		return
	}
	client, actions, enabled := s.prioTicketClient(r.Context())
	if !enabled {
		http.Error(w, "Persönliche Jira-Anmeldung erforderlich.", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	action := r.PostForm.Get("action")
	q := prioAgendaParams(r.PostForm)
	if action == "parent" {
		q.Del("parent")
	}
	if parentFilter := r.PostForm.Get("parent-filter"); parentFilter != "" {
		q.Set("parent", parentFilter)
	}
	if !q.Has("preset") {
		q.Set("preset", prioDefaultPreset)
	}
	key := r.PostForm.Get("key")
	var result prioTicketResult
	ctx := r.Context()
	identity, _ := JiraIdentityFromContext(ctx)
	author := jira.Person{AccountID: identity.AccountID}
	var err error
	if action == "epic" {
		key, err = createPrioEpic(ctx, actions, r.PostForm, author)
	} else {
		detail, found, readErr := s.rollups.IssueDetail(key)
		if readErr != nil {
			s.renderError(w)
			return
		}
		if !dcaiIssueKey.MatchString(key) || !found {
			http.Error(w, "Dieses DCAI-Ticket ist im Jira-Abbild nicht verfügbar.", http.StatusNotFound)
			return
		}
		q.Set("ticket", key)
		err = s.mutatePrioTicket(ctx, actions, detail, action, r.PostForm, author)
	}
	switch {
	case errors.Is(err, errPrioFieldUnchanged):
	case err != nil:
		result.Message = err.Error()
	default:
		s.invalidatePrioSearches(identity.AccountID)
		if dcaiIssueKey.MatchString(key) {
			refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			issue, refreshErr := s.prioRefresher.RefreshIssue(refreshCtx, client, key)
			cancel()
			if refreshErr == nil {
				result.Change = key + " · " + prioMutationLabel(action, issue)
				if action == "epic" {
					result.CreatedHref = prioHref(q, "ticket", key)
				}
				break
			}
			log.Printf("web: refresh after Prio ticket mutation %s: %v", key, refreshErr)
		}
		result.Message = "Jira hat die Änderung gespeichert. Das lokale Abbild konnte noch nicht aktualisiert werden."
		result.Change = key + " · " + prioMutationLabel(action, jira.Issue{})
	}
	name := "prio.html"
	if r.Header.Get("HX-Request") == "true" {
		name = "prio-boosted"
		if result.Message != "" && result.Change == "" {
			// Keep the form, typed values, focus and scroll in place on a Jira rejection.
			target := "#prio-ticket-feedback"
			switch action {
			case "epic":
				target = "#prio-epic-feedback"
			case "comment":
				target = "#prio-comment-feedback"
			case "summary", "description", "priority", "assignee", "size", "labels", "parent":
				target = "#prio-ticket-feedback-" + action
			}
			w.Header().Set("HX-Retarget", target)
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("HX-Push-Url", "false")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := s.templates.ExecuteTemplate(w, "prio-ticket-feedback", result); err != nil {
				s.renderError(w)
			}
			return
		}
	}
	s.renderPrioWith(w, context.WithValue(ctx, prioTicketResultKey{}, result), q, name, prioEdit{})
}

// A successful write can change Jira preset membership and ordering. The
// response must re-run the signed-in person's search against their own Jira.
func (s *Server) invalidatePrioSearches(accountID string) {
	s.prioKeysCache.mu.Lock()
	defer s.prioKeysCache.mu.Unlock()
	for query := range s.prioKeysCache.entries {
		if query.account == accountID {
			delete(s.prioKeysCache.entries, query)
		}
	}
}

var errPrioFieldUnchanged = errors.New("unchanged")

func (s *Server) mutatePrioTicket(ctx context.Context, actions jira.IssueActions, detail store.IssueDetail, action string, form url.Values, author jira.Person) error {
	currentDescription, err := actions.ReadPrioIssueDescription(ctx, detail.Key)
	if err != nil {
		return err
	}
	switch action {
	case "comment":
		comment := strings.TrimSpace(form.Get("comment"))
		if comment == "" {
			return errors.New("Bitte einen Kommentar eingeben.")
		}
		return actions.AddIssueComment(ctx, detail.Key, jira.PlainADF(comment), author)
	case "transition":
		return transitionPrioTicket(ctx, actions, detail.Key, form.Get("transition"), author)
	case "summary", "description", "priority", "assignee", "size", "labels", "parent":
	default:
		return errors.New("Unbekannte Ticket-Aktion.")
	}
	if action == "description" && form.Get("description-revision") != descriptionRevision(detail) {
		return errors.New("Die Beschreibung hat sich geändert. Bitte das Ticket neu laden und die Eingabe prüfen.")
	}
	fields, err := actions.FetchEditFields(ctx, detail.Key)
	if err != nil {
		return err
	}
	for _, field := range s.prioTicketForms(detail, fields) {
		if field.Name != action {
			continue
		}
		if !form.Has(action) {
			return errors.New("Bitte einen Wert für dieses Feld eingeben.")
		}
		value := strings.ReplaceAll(form.Get(action), "\r\n", "\n")
		if value == field.Current {
			return errPrioFieldUnchanged
		}
		payload, err := prioFieldValue(field, fields[prioJiraField(action)], value, detail.Key)
		if err != nil {
			return err
		}
		if action == "description" {
			text, plain := jira.ADFText(currentDescription)
			if !plain || text != field.Current {
				return errors.New("Die Beschreibung hat sich in Jira geändert. Bitte in Jira prüfen.")
			}
		}
		return actions.EditIssueField(ctx, detail.Key, prioJiraField(action), payload, author)
	}
	return errors.New("Jira bietet dieses Feld hier nicht zur Bearbeitung an.")
}

func transitionPrioTicket(ctx context.Context, actions jira.IssueActions, key, id string, author jira.Person) error {
	offered, err := actions.FetchIssueTransitions(ctx, key)
	if err != nil {
		return err
	}
	for _, transition := range offered {
		if transition.ID == id {
			if prioTransitionView(transition).Complex {
				return errors.New("Bitte diesen Statusübergang in Jira ausführen.")
			}
			return actions.ApplyIssueTransition(ctx, key, id, author)
		}
	}
	return errors.New("Jira bietet diesen Statusübergang nicht mehr an.")
}

func createPrioEpic(ctx context.Context, actions jira.IssueActions, form url.Values, author jira.Person) (string, error) {
	summary := strings.TrimSpace(form.Get("epic-summary"))
	if summary == "" {
		return "", errors.New("Bitte einen Epic-Titel eingeben.")
	}
	var description = jira.PlainADF(form.Get("epic-description"))
	if strings.TrimSpace(form.Get("epic-description")) == "" {
		description = nil
	}
	return actions.CreatePrioEpic(ctx, summary, description, author)
}

func prioMutationLabel(action string, issue jira.Issue) string {
	switch action {
	case "comment":
		return "Kommentar hinzugefügt"
	case "epic":
		return "Epic angelegt"
	case "transition":
		if issue.Status != "" {
			return "Status → " + issue.Status
		}
		return "Status geändert"
	case "priority":
		if issue.Priority != "" {
			return "Priorität " + prioLevelLabel(issue.Priority)
		}
		return "Priorität geändert"
	case "summary":
		return "Titel geändert"
	case "description":
		return "Beschreibung geändert"
	case "assignee":
		return "Zuweisung geändert"
	case "size":
		return "Schätzung geändert"
	case "labels":
		return "Labels geändert"
	case "parent":
		return "Parent geändert"
	default:
		return "Ticket geändert"
	}
}
