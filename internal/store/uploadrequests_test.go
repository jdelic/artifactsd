package store

import (
	"errors"
	"testing"
	"time"
)


func TestUploadRequests_CRUD(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "inbox")

	label := "Test Request"
	ur := &UploadRequest{
		RequestID:           "urq_testcrud1234567",
		CollectionID:        c.ID,
		TargetFolder:        "uploads",
		Label:               &label,
		FileMetadata:        []byte(`{}`),
		AllowUploaderAccess: true,
		SingleUse:           false,
		Status:              UploadRequestActive,
	}
	if err := s.CreateUploadRequest(ctx, ur); err != nil {
		t.Fatalf("CreateUploadRequest: %v", err)
	}
	if ur.ID == 0 {
		t.Fatal("ID not set after create")
	}
	if !ur.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", ur.CreatedAt, fixedNow)
	}

	// GetUploadRequest
	got, err := s.GetUploadRequest(ctx, ur.RequestID)
	if err != nil {
		t.Fatalf("GetUploadRequest: %v", err)
	}
	if got.CollectionID != c.ID || got.TargetFolder != "uploads" {
		t.Errorf("unexpected fields: %+v", got)
	}
	if got.Label == nil || *got.Label != label {
		t.Errorf("Label = %v, want %q", got.Label, label)
	}
	if !got.AllowUploaderAccess {
		t.Error("AllowUploaderAccess should be true")
	}

	// GetUploadRequestByID
	byID, err := s.GetUploadRequestByID(ctx, ur.ID)
	if err != nil || byID.RequestID != ur.RequestID {
		t.Fatalf("GetUploadRequestByID: %v %+v", err, byID)
	}

	// ListUploadRequests (all)
	list, err := s.ListUploadRequests(ctx, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListUploadRequests all: %v (len=%d)", err, len(list))
	}

	// ListUploadRequests (by collection)
	list, err = s.ListUploadRequests(ctx, &c.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListUploadRequests by collection: %v (len=%d)", err, len(list))
	}

	// UpdateUploadRequest
	newLabel := "Updated"
	ur.Label = &newLabel
	if err := s.UpdateUploadRequest(ctx, ur); err != nil {
		t.Fatalf("UpdateUploadRequest: %v", err)
	}
	reloaded, _ := s.GetUploadRequest(ctx, ur.RequestID)
	if reloaded.Label == nil || *reloaded.Label != "Updated" {
		t.Errorf("label after update = %v, want Updated", reloaded.Label)
	}

	// CloseUploadRequest
	if err := s.CloseUploadRequest(ctx, ur.RequestID, UploadRequestRevoked, fixedNow); err != nil {
		t.Fatalf("CloseUploadRequest: %v", err)
	}
	closed, _ := s.GetUploadRequest(ctx, ur.RequestID)
	if closed.Status != UploadRequestRevoked {
		t.Errorf("status after close = %q, want %q", closed.Status, UploadRequestRevoked)
	}

	// GetUploadRequest on unknown id
	_, err = s.GetUploadRequest(ctx, "urq_doesnotexist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUploadRequest missing: want ErrNotFound, got %v", err)
	}
}

func TestUploadRequests_IsActive(t *testing.T) {
	now := fixedNow
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		status    string
		expiresAt *time.Time
		want      bool
	}{
		{UploadRequestActive, nil, true},
		{UploadRequestActive, &future, true},
		{UploadRequestActive, &past, false},
		{UploadRequestClosed, nil, false},
		{UploadRequestRevoked, nil, false},
		{UploadRequestExpired, nil, false},
	}
	for _, tt := range tests {
		ur := &UploadRequest{Status: tt.status, ExpiresAt: tt.expiresAt}
		if got := ur.IsActive(now); got != tt.want {
			t.Errorf("status=%s expires=%v: IsActive=%v, want %v", tt.status, tt.expiresAt, got, tt.want)
		}
	}
}

func TestUploadRequests_CommitSubmission(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "inbox")

	ur := &UploadRequest{
		RequestID:    "urq_submittest1234567",
		CollectionID: c.ID,
		FileMetadata: []byte(`{}`),
		Status:       UploadRequestActive,
	}
	if err := s.CreateUploadRequest(ctx, ur); err != nil {
		t.Fatalf("CreateUploadRequest: %v", err)
	}

	sub := &UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    "sub_test01",
		FileCount:       2,
		TotalBytes:      1024,
	}
	if err := s.CommitUploadRequestSubmission(ctx, sub, false, fixedNow); err != nil {
		t.Fatalf("CommitUploadRequestSubmission: %v", err)
	}

	reloaded, _ := s.GetUploadRequestByID(ctx, ur.ID)
	if reloaded.FilesUploaded != 2 {
		t.Errorf("FilesUploaded = %d, want 2", reloaded.FilesUploaded)
	}
	if reloaded.Status != UploadRequestActive {
		t.Errorf("status = %q, want active", reloaded.Status)
	}

	subs, err := s.ListUploadRequestSubmissions(ctx, ur.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("ListUploadRequestSubmissions: %v (len=%d)", err, len(subs))
	}
	if subs[0].FileCount != 2 || subs[0].TotalBytes != 1024 {
		t.Errorf("unexpected submission: %+v", subs[0])
	}
}

func TestUploadRequests_CommitSubmission_SingleUse(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "inbox")

	ur := &UploadRequest{
		RequestID:    "urq_singleuse12345678",
		CollectionID: c.ID,
		FileMetadata: []byte(`{}`),
		SingleUse:    true,
		Status:       UploadRequestActive,
	}
	if err := s.CreateUploadRequest(ctx, ur); err != nil {
		t.Fatalf("CreateUploadRequest: %v", err)
	}

	sub := &UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    "sub_singleuse",
		FileCount:       1,
		TotalBytes:      512,
	}
	if err := s.CommitUploadRequestSubmission(ctx, sub, true, fixedNow); err != nil {
		t.Fatalf("CommitUploadRequestSubmission: %v", err)
	}

	// Should now be closed
	reloaded, _ := s.GetUploadRequestByID(ctx, ur.ID)
	if reloaded.Status != UploadRequestClosed {
		t.Errorf("status after single_use submit = %q, want %q", reloaded.Status, UploadRequestClosed)
	}

	// Second submission should fail: not active
	sub2 := &UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    "sub_singleuse2",
		FileCount:       1,
		TotalBytes:      256,
	}
	err := s.CommitUploadRequestSubmission(ctx, sub2, true, fixedNow)
	if !errors.Is(err, ErrUploadRequestNotActive) {
		t.Errorf("second submit: want ErrUploadRequestNotActive, got %v", err)
	}
}

func TestUploadRequests_CommitSubmission_QuotaExceeded(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "inbox")

	maxFiles := int64(2)
	ur := &UploadRequest{
		RequestID:    "urq_quotatest12345678",
		CollectionID: c.ID,
		FileMetadata: []byte(`{}`),
		MaxFiles:     &maxFiles,
		Status:       UploadRequestActive,
	}
	if err := s.CreateUploadRequest(ctx, ur); err != nil {
		t.Fatalf("CreateUploadRequest: %v", err)
	}

	sub := &UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    "sub_quota1",
		FileCount:       2,
		TotalBytes:      100,
	}
	if err := s.CommitUploadRequestSubmission(ctx, sub, false, fixedNow); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	sub2 := &UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    "sub_quota2",
		FileCount:       1,
		TotalBytes:      50,
	}
	err := s.CommitUploadRequestSubmission(ctx, sub2, false, fixedNow)
	if !errors.Is(err, ErrUploadRequestQuotaExceeded) {
		t.Errorf("over quota: want ErrUploadRequestQuotaExceeded, got %v", err)
	}
}

func TestUploadRequests_JanitorExpiry(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "inbox")

	past := fixedNow.Add(-time.Hour)
	future := fixedNow.Add(time.Hour)
	ur1 := &UploadRequest{
		RequestID:    "urq_expired12345678901",
		CollectionID: c.ID,
		FileMetadata: []byte(`{}`),
		ExpiresAt:    &past,
		Status:       UploadRequestActive,
	}
	ur2 := &UploadRequest{
		RequestID:    "urq_notexpired1234567",
		CollectionID: c.ID,
		FileMetadata: []byte(`{}`),
		ExpiresAt:    &future,
		Status:       UploadRequestActive,
	}
	for _, ur := range []*UploadRequest{ur1, ur2} {
		if err := s.CreateUploadRequest(ctx, ur); err != nil {
			t.Fatalf("CreateUploadRequest: %v", err)
		}
	}

	expired, err := s.ListExpiredUploadRequests(ctx, fixedNow, 100)
	if err != nil {
		t.Fatalf("ListExpiredUploadRequests: %v", err)
	}
	if len(expired) != 1 || expired[0].RequestID != ur1.RequestID {
		t.Errorf("expired list = %v, want [%s]", expired, ur1.RequestID)
	}

	if err := s.SetUploadRequestStatus(ctx, ur1.RequestID, UploadRequestExpired); err != nil {
		t.Fatalf("SetUploadRequestStatus: %v", err)
	}
	reloaded, _ := s.GetUploadRequest(ctx, ur1.RequestID)
	if reloaded.Status != UploadRequestExpired {
		t.Errorf("status = %q, want expired", reloaded.Status)
	}
}
