package server

import (
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// subFlashSep joins the values a subtask success line names.
const subFlashSep = "\x1f"

// redirectSavedSub redirects to a page and has it show a success line about a
// subtask. The values it names (subtask, task, person, stars) travel in the
// server's memory behind a random token, like the entries of a list.
func (s *server) redirectSavedSub(w http.ResponseWriter, r *http.Request, to, key string, vals ...string) {
	s.redirectSavedText(w, r, to, key, strings.Join(vals, subFlashSep))
}

// subFlashText fills a subtask success line from the values stored with
// redirectSavedSub: subtask title, task title, person name, stars.
func (s *server) subFlashText(lang, key, token string) string {
	raw, ok := s.flashTexts.take(token, s.now())
	if !ok {
		return ""
	}
	v := strings.Split(raw, subFlashSep)
	for len(v) < 4 {
		v = append(v, "")
	}
	return s.i18n.Format(lang, i18n.Msg(key, "title", v[0], "task", v[1], "person", v[2], "stars", v[3]))
}

// starsKey picks the plural variant of a text about stars: none, one or many.
func starsKey(base string, n int) string {
	switch n {
	case 0:
		return base + ".zero"
	case 1:
		return base + ".one"
	}
	return base + ".other"
}

// taskAndOwner loads a task and the person it belongs to.
func (s *server) taskAndOwner(p principal, id string) (task.Task, store.Person, error) {
	t, err := s.db.Task(p.household.ID, id)
	if err != nil {
		return task.Task{}, store.Person{}, err
	}
	owner, err := s.db.Person(p.household.ID, t.PersonID)
	return t, owner, err
}

func (s *server) subtaskFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, errNotYourTask):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	default:
		s.fail(w, "subtask", err)
	}
}

// subtaskTitle is the title of one subtask of a task, "" if there is none.
func (s *server) subtaskTitle(p principal, taskID, subID string) string {
	list, err := s.db.Subtasks(p.household.ID, taskID, s.today(p))
	if err != nil {
		return ""
	}
	for _, k := range list {
		if k.ID == subID {
			return k.Title
		}
	}
	return ""
}

// subtaskForm is what was typed into the form of a subtask that was refused.
type subtaskForm struct {
	ID           string // the subtask whose edit was refused; "" for the add form
	Title, Emoji string
	Error, Field string // text key and the field it belongs to; Field "" shows it above the page
}

func (s *server) handleSubtaskAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	title, emoji := r.PostFormValue("title"), r.PostFormValue("emoji")
	t, owner, err := s.taskAndOwner(p, id)
	if err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	wasDone, _ := s.db.TaskDone(id, s.today(p))
	switch added, err := s.db.AddSubtask(p.household.ID, id, task.Subtask{Title: title, Emoji: emoji}, s.today(p), s.now()); {
	case err == nil:
		key := "flash.sub_added"
		if wasDone {
			key = "flash.sub_added_done"
		}
		s.redirectSavedSub(w, r, "/tasks/"+id+"#subs", key, added.Title, t.Title, owner.Name)
	case errors.Is(err, store.ErrInvalid):
		s.renderTaskPageWith(w, r, p, http.StatusUnprocessableEntity, nil, &subtaskForm{Title: title, Emoji: emoji, Error: subtaskErrorKey(err), Field: subtaskErrorField(err)})
	default:
		s.subtaskFailed(w, r, err)
	}
}

// subtaskErrorField is the form field a refused subtask belongs to.
func subtaskErrorField(err error) string {
	var bad *task.InvalidError
	if errors.As(err, &bad) && (bad.Field == "title" || bad.Field == "emoji") {
		return bad.Field
	}
	return ""
}

func subtaskErrorKey(err error) string {
	var bad *task.InvalidError
	if errors.As(err, &bad) {
		switch bad.Field {
		case "title", "emoji", "subtasks":
			return "tasks.error.sub_" + bad.Field
		}
	}
	return "tasks.error.sub_invalid"
}

func (s *server) handleSubtaskSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id, sub := r.PathValue("id"), r.PathValue("sub_id")
	title, emoji := r.PostFormValue("title"), r.PostFormValue("emoji")
	switch err := s.db.UpdateSubtask(p.household.ID, id, sub, title, emoji); {
	case err == nil:
		s.redirectSavedSub(w, r, "/tasks/"+id+"#subs", "flash.sub_saved", strings.TrimSpace(title))
	case errors.Is(err, store.ErrInvalid):
		s.renderTaskPageWith(w, r, p, http.StatusUnprocessableEntity, nil, &subtaskForm{ID: sub, Title: title, Emoji: emoji, Error: subtaskErrorKey(err), Field: subtaskErrorField(err)})
	default:
		s.subtaskFailed(w, r, err)
	}
}

func (s *server) handleSubtaskMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id, sub := r.PathValue("id"), r.PathValue("sub_id")
	dir := r.PostFormValue("direction")
	if dir != "up" && dir != "down" {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	title := s.subtaskTitle(p, id, sub)
	if err := s.db.MoveSubtask(p.household.ID, id, sub, dir == "down"); err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	s.redirectSavedSub(w, r, "/tasks/"+id+"#subs", "flash.sub_moved", title)
}

func (s *server) handleSubtaskDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id, sub := r.PathValue("id"), r.PathValue("sub_id")
	t, owner, err := s.taskAndOwner(p, id)
	if err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	title := s.subtaskTitle(p, id, sub)
	ch, err := s.db.DeleteSubtask(p.household.ID, id, sub, s.today(p), p.person.ID, s.now())
	if err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	key := "flash.sub_removed"
	if ch.TaskChanged && ch.TaskDone {
		key = starsKey("flash.sub_removed_last", ch.Stars)
	}
	s.redirectSavedSub(w, r, "/tasks/"+id+"#subs", key, title, t.Title, owner.Name, strconv.Itoa(ch.Stars))
}

// handleSubtaskDone ticks a subtask off or open again for today: a parent on
// the task page, a child on their own tasks page (ADR 0015).
func (s *server) handleSubtaskDone(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	id, sub := r.PathValue("id"), r.PathValue("sub_id")
	done := r.PostFormValue("done") == "1"
	title := s.subtaskTitle(p, id, sub)
	t, owner, err := s.taskAndOwner(p, id)
	if err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	ch, err := s.tickSubtask(p, id, sub, done)
	if err != nil {
		s.subtaskFailed(w, r, err)
		return
	}
	if p.person.Role == store.RoleChild {
		key := "flash.sub_open"
		if done {
			key = "flash.sub_done"
			if ch.TaskChanged && ch.TaskDone {
				key = "flash.task_done"
			}
		}
		if key == "flash.task_done" {
			s.redirectSaved(w, r, "/tasks", s.wonKey(p, ch.Badges, key))
		} else {
			s.redirectSavedSub(w, r, "/tasks", key, title)
		}
		return
	}
	var key string
	switch {
	case done && ch.TaskChanged && ch.TaskDone:
		key = starsKey("flash.sub_done_last", ch.Stars)
	case done:
		key = "flash.sub_done"
	case ch.TaskChanged:
		key = starsKey("flash.sub_open_task", ch.Stars)
	default:
		key = "flash.sub_open"
	}
	s.redirectSavedSub(w, r, "/tasks/"+id+"#subs", key, title, t.Title, owner.Name, strconv.Itoa(ch.Stars))
}

// taskCreatedHTML is the success line after a new task: it names the task and
// links to its subtasks, which are added on its own page.
func (s *server) taskCreatedHTML(lang, id string) (string, bool) {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return "", false
	}
	t, owner, err := s.taskAndOwner(principal{household: h}, id)
	if err != nil {
		return "", false
	}
	const mark = "@@LINK@@"
	text := html.EscapeString(s.i18n.Format(lang, i18n.Msg("flash.task_created", "title", t.Title, "name", owner.Name, "link", mark)))
	link := `<a href="/tasks/` + html.EscapeString(t.ID) + `#subs">` + html.EscapeString(s.i18n.T(lang, "flash.task_created.link")) + `</a>`
	return strings.Replace(text, mark, link, 1), true
}

// subRow is a subtask on the task page: its place, state and what removing it does.
type subRow struct {
	subView
	CanUp, CanDown bool
	Remove         string // the sentence under "Change or remove"
}

// subtaskCard adds what the "Subtasks" card of the task page shows: every
// subtask with its state today, a sentence on what the last tick does and, per
// row, what removing it does before the parent confirms.
func (s *server) subtaskCard(data map[string]any, p principal, t task.Task, owner, lang string) error {
	list, err := s.db.Subtasks(p.household.ID, t.ID, s.today(p))
	if err != nil {
		return err
	}
	view := taskView{}.withSubs(list)
	done, total := view.SubsDone, len(list)
	taskDone, _ := data["Task"].(taskView)
	say := func(base string, kv ...string) string {
		return s.i18n.Format(lang, i18n.Msg(starsKey(base, t.Stars), append([]string{"task", t.Title, "name", owner, "stars", strconv.Itoa(t.Stars)}, kv...)...))
	}
	rows := make([]subRow, 0, total)
	for i, k := range view.Subs {
		row := subRow{subView: k, CanUp: i > 0, CanDown: i < total-1}
		row.Remove = s.i18n.T(lang, "tasks.sub.remove.explain")
		if total >= 2 && !k.Done && total-done == 1 && taskDone.Due && !taskDone.Done {
			row.Remove = say("tasks.sub.remove.last", "title", k.Title)
		}
		rows = append(rows, row)
	}
	data["Subs"], data["SubsDone"], data["SubsTotal"] = rows, done, total
	data["SubsFull"] = total >= task.MaxSubtasks
	data["SubsExplain"] = say("tasks.subs.explain")
	data["SubsEmpty"] = s.i18n.Format(lang, i18n.Msg("tasks.subs.empty", "task", t.Title, "name", owner))
	data["SubsLimit"] = s.i18n.Format(lang, i18n.Msg("tasks.subs.limit", "task", t.Title, "max", strconv.Itoa(task.MaxSubtasks)))
	return nil
}
