package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

type rewardJSON struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Symbol         string   `json:"symbol"`
	Picture        *string  `json:"picture"`
	PictureSource  string   `json:"picture_source"`
	Price          int      `json:"price"`
	LimitPeriod    string   `json:"limit_period"`
	LimitCount     *int     `json:"limit_count"`
	WholeFamily    bool     `json:"whole_family"`
	PersonIDs      []string `json:"person_ids"`
	Archived       bool     `json:"archived"`
	ArchivedAt     *string  `json:"archived_at"`
	ArchivedReason *string  `json:"archived_reason"`
	// Redeemed and CreatedBy are for parents: nothing about siblings reaches a child.
	Redeemed  *int    `json:"redeemed"`
	CreatedBy *string `json:"created_by"`
	CreatedAt string  `json:"created_at"`
	// What the caller (a child) can do with it now; null for a parent.
	Affordable     *bool   `json:"affordable"`
	LimitReached   *bool   `json:"limit_reached"`
	AvailableAgain *string `json:"available_again"`
}

func (s *server) rewardToJSON(p principal, r store.Reward, redeemed int, st *rewardState) rewardJSON {
	loc := p.location()
	j := rewardJSON{ID: r.ID, Name: r.Name, Symbol: r.Symbol, PictureSource: r.PicSource, Price: r.Price, LimitPeriod: string(r.Limit.Period),
		WholeFamily: len(r.PersonIDs) == 0 && r.ArchivedReason != store.ArchivedAudienceGone, PersonIDs: nonNil(r.PersonIDs), Archived: r.Archived(), CreatedAt: r.CreatedAt.In(loc).Format(time.RFC3339)}
	if r.HasPicture {
		u := rewardPictureAPI(r)
		j.Picture = &u
	}
	if r.Limit.Period != reward.NoLimit {
		n := r.Limit.Count
		j.LimitCount = &n
	}
	if r.Archived() {
		at := r.ArchivedAt.In(loc).Format(time.RFC3339)
		j.ArchivedAt = &at
		if r.ArchivedReason != "" {
			reason := r.ArchivedReason
			j.ArchivedReason = &reason
		}
	}
	if st != nil {
		a, l := st.Affordable, st.LimitReached
		j.Affordable, j.LimitReached = &a, &l
		if st.LimitReached {
			again := st.AvailableAgain.In(loc).Format(time.RFC3339)
			j.AvailableAgain = &again
		}
	} else {
		j.Redeemed = &redeemed
		if r.CreatedBy != "" {
			by := r.CreatedBy
			j.CreatedBy = &by
		}
	}
	return j
}

func rewardPictureAPI(r store.Reward) string {
	return "/rewards/" + r.ID + "/picture?v=" + strconv.FormatInt(r.PictureAt.Unix(), 10)
}

// rewardsView renders rewards for the caller: a parent sees the catalogue with
// counts; a child sees their own offered ones with what they can afford.
func (s *server) rewardsView(p principal, list []store.Reward) ([]rewardJSON, error) {
	out := make([]rewardJSON, 0, len(list))
	if isChild(p) {
		_, states, err := s.rewardStates(p, p.person.ID, list)
		if err != nil {
			return nil, err
		}
		for _, r := range list {
			st := states[r.ID]
			out = append(out, s.rewardToJSON(p, r, 0, &st))
		}
		return out, nil
	}
	counts, err := s.redeemedCounts(p)
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		out = append(out, s.rewardToJSON(p, r, counts[r.ID], nil))
	}
	return out, nil
}

func (s *server) apiListRewards(w http.ResponseWriter, _ *http.Request, p principal) {
	var list []store.Reward
	var err error
	if isChild(p) {
		list, err = s.db.RewardsFor(p.household.ID, p.person.ID)
	} else {
		list, err = s.db.Rewards(p.household.ID)
	}
	if err != nil {
		s.fail(w, "list rewards", err)
		return
	}
	out, err := s.rewardsView(p, list)
	if err != nil {
		s.fail(w, "reward states", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// rewardFor loads a reward the caller may see: a child only an offered one meant for them.
func (s *server) rewardFor(w http.ResponseWriter, p principal, id string) (store.Reward, bool) {
	r, err := s.db.Reward(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && isChild(p) && (r.Archived() || !r.MeantFor(p.person.ID))) {
		writeError(w, http.StatusNotFound, "reward not found")
		return store.Reward{}, false
	}
	if err != nil {
		s.fail(w, "get reward", err)
		return store.Reward{}, false
	}
	return r, true
}

func (s *server) writeReward(w http.ResponseWriter, p principal, status int, r store.Reward) {
	out, err := s.rewardsView(p, []store.Reward{r})
	if err != nil {
		s.fail(w, "reward view", err)
		return
	}
	writeJSON(w, status, out[0])
}

func (s *server) apiGetReward(w http.ResponseWriter, r *http.Request, p principal) {
	if rw, ok := s.rewardFor(w, p, r.PathValue("id")); ok {
		s.writeReward(w, p, http.StatusOK, rw)
	}
}

type rewardWrite struct {
	Name          *string   `json:"name"`
	Symbol        *string   `json:"symbol"`
	Price         *int      `json:"price"`
	LimitPeriod   *string   `json:"limit_period"`
	LimitCount    *int      `json:"limit_count"`
	WholeFamily   *bool     `json:"whole_family"`
	PersonIDs     *[]string `json:"person_ids"`
	RemovePicture bool      `json:"remove_picture"`
}

// audience reads who the reward is meant for. given is false when the request says nothing about it.
func (in rewardWrite) audience() (ids []string, given bool, field string) {
	whole := in.WholeFamily != nil && *in.WholeFamily
	var people []string
	if in.PersonIDs != nil {
		people = *in.PersonIDs
	}
	switch {
	case in.WholeFamily == nil && in.PersonIDs == nil:
		return nil, false, ""
	case whole && len(people) > 0, !whole && len(people) == 0:
		return nil, true, "person"
	}
	return people, true, ""
}

// mergeReward applies the fields the request gives to cur. A field that is
// wrong comes back as its name.
func (in rewardWrite) mergeReward(cur store.Reward) (store.Reward, string) {
	if in.Name != nil {
		cur.Name = *in.Name
	}
	if in.Symbol != nil {
		cur.Symbol = *in.Symbol
	}
	if in.Price != nil {
		cur.Price = *in.Price
	}
	if in.LimitPeriod != nil {
		cur.Limit.Period = reward.Period(*in.LimitPeriod)
		if cur.Limit.Period == reward.NoLimit || cur.Limit.Period == "" {
			cur.Limit = reward.Limit{Period: reward.NoLimit}
		} else if in.LimitCount == nil && cur.Limit.Count == 0 {
			return cur, "limit_count"
		}
	}
	if in.LimitCount != nil {
		cur.Limit.Count = *in.LimitCount
	}
	if ids, given, field := in.audience(); field != "" {
		return cur, field
	} else if given {
		cur.PersonIDs = ids
	}
	return cur, ""
}

// rewardError answers a refused reward: a field with its sentence, or a conflict.
func (s *server) rewardError(w http.ResponseWriter, what string, err error) {
	var fe *store.FieldError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "reward not found")
	case errors.Is(err, store.ErrRedeemed):
		writeError(w, http.StatusConflict, "this reward was redeemed: archive it instead of deleting it")
	case errors.Is(err, store.ErrArchived):
		writeError(w, http.StatusConflict, "this reward is archived: offer it again to change it")
	case errors.As(err, &fe):
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiCreateReward(w http.ResponseWriter, r *http.Request, p principal) {
	var in rewardWrite
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Name == nil || in.Price == nil {
		field := "name"
		if in.Name != nil {
			field = "price"
		}
		writeFieldError(w, http.StatusUnprocessableEntity, field, "reward "+field+": required")
		return
	}
	if _, given, _ := in.audience(); !given {
		writeFieldError(w, http.StatusUnprocessableEntity, "person", "say who it is for: whole_family true or person_ids")
		return
	}
	cur, field := in.mergeReward(store.Reward{})
	if field != "" {
		writeFieldError(w, http.StatusUnprocessableEntity, field, "reward "+field+": invalid")
		return
	}
	created, err := s.db.CreateReward(p.household.ID, p.person.ID, cur, nil)
	if err != nil {
		s.rewardError(w, "create reward", err)
		return
	}
	s.writeReward(w, p, http.StatusCreated, created)
}

func (s *server) apiUpdateReward(w http.ResponseWriter, r *http.Request, p principal) {
	var in rewardWrite
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur, err := s.db.Reward(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.rewardError(w, "get reward", err)
		return
	}
	next, field := in.mergeReward(cur)
	if field != "" {
		writeFieldError(w, http.StatusUnprocessableEntity, field, "reward "+field+": invalid")
		return
	}
	change := store.KeepPicture
	if (in.Symbol != nil && *in.Symbol != "") || in.RemovePicture {
		change = store.RemovePicture
	}
	saved, err := s.db.UpdateReward(p.household.ID, next, change, nil)
	if err != nil {
		s.rewardError(w, "update reward", err)
		return
	}
	s.writeReward(w, p, http.StatusOK, saved)
}

func (s *server) apiDeleteReward(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.DeleteReward(p.household.ID, r.PathValue("id")); err != nil {
		s.rewardError(w, "delete reward", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiArchiveReward(w http.ResponseWriter, r *http.Request, p principal) {
	saved, err := s.db.ArchiveReward(p.household.ID, r.PathValue("id"), s.now())
	if err != nil {
		s.rewardError(w, "archive reward", err)
		return
	}
	s.writeReward(w, p, http.StatusOK, saved)
}

// apiRestoreReward offers an archived reward again. A reward the box archived
// because its audience left needs a new one in the body.
func (s *server) apiRestoreReward(w http.ResponseWriter, r *http.Request, p principal) {
	var in rewardWrite
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) { // no body: nothing about the audience
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ids, given, field := in.audience()
	if field != "" {
		writeFieldError(w, http.StatusUnprocessableEntity, field, "choose the whole family or people")
		return
	}
	var aud *store.Audience
	if given {
		aud = &store.Audience{Whole: len(ids) == 0, PersonIDs: ids}
	}
	saved, err := s.db.RestoreReward(p.household.ID, r.PathValue("id"), aud)
	if err != nil {
		s.rewardError(w, "restore reward", err)
		return
	}
	s.writeReward(w, p, http.StatusOK, saved)
}

func (s *server) apiGetRewardPicture(w http.ResponseWriter, r *http.Request, p principal) {
	rw, ok := s.rewardFor(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	b, err := s.db.RewardPicture(p.household.ID, rw.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "reward or picture not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// apiPutRewardPicture takes a photo as the request body.
func (s *server) apiPutRewardPicture(w http.ResponseWriter, r *http.Request, p principal) {
	cur, err := s.db.Reward(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.rewardError(w, "get reward", err)
		return
	}
	res, err := picture.ProcessMax(r.Context(), http.MaxBytesReader(w, r.Body, picture.MaxBytes+1), picture.MaxSide)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			err = &picture.TooLargeError{Bytes: tooBig.Limit}
		}
		s.pictureErrorJSON(w, "picture", err)
		return
	}
	pic := &store.ArticlePicture{JPEG: res.JPEG, Source: "upload", FromW: res.FromW, FromH: res.FromH, FromBytes: res.FromBytes}
	saved, err := s.db.UpdateReward(p.household.ID, cur, store.SetPicture, pic)
	if err != nil {
		s.rewardError(w, "set reward picture", err)
		return
	}
	s.writeReward(w, p, http.StatusOK, saved)
}
