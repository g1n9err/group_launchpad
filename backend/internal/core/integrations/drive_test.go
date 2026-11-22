package integrations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── fakes for Drive permission tests ───────────────────────────────────────

type fakeDriveGateway struct {
	GeneratedIDs []string
	Folders      map[string]string     // id → name (created folders)
	Permissions  map[string][]fakePerm // folderID → [{email, role}]
	Reject       map[fakePerm]bool
}

type fakePerm struct {
	Email string
	Role  string
}

func newFakeDriveGateway() *fakeDriveGateway {
	return &fakeDriveGateway{
		GeneratedIDs: []string{"D1", "D2", "D3", "D4", "D5", "D6"},
		Folders:      map[string]string{},
		Permissions:  map[string][]fakePerm{},
	}
}

func (f *fakeDriveGateway) GenerateFolderID(_ context.Context, _ string) (string, error) {
	if len(f.GeneratedIDs) == 0 {
		return "", ErrSlackUnavailable // any error
	}
	id := f.GeneratedIDs[0]
	f.GeneratedIDs = f.GeneratedIDs[1:]
	return id, nil
}

func (f *fakeDriveGateway) EnsureFolder(_ context.Context, _, id, name, parentID string) error {
	f.Folders[id] = name
	return nil
}

func (f *fakeDriveGateway) GrantAccess(_ context.Context, _, folderID, email, role string) error {
	if f.Reject[fakePerm{Email: email, Role: role}] {
		return errors.New("permission rejected")
	}
	f.Permissions[folderID] = append(f.Permissions[folderID], fakePerm{Email: email, Role: role})
	return nil
}

func (f *fakeDriveGateway) RevokeAccess(_ context.Context, _, folderID, email string) error {
	permissions := f.Permissions[folderID]
	kept := permissions[:0]
	for _, permission := range permissions {
		if permission.Email != email {
			kept = append(kept, permission)
		}
	}
	f.Permissions[folderID] = kept
	return nil
}

type fakeDriveStore struct {
	resources map[string]GroupResource // key: name
}

func newFakeDriveStore() *fakeDriveStore {
	return &fakeDriveStore{resources: map[string]GroupResource{}}
}

func (s *fakeDriveStore) GetResource(_ context.Context, _ uuid.UUID, _, _, name string) (GroupResource, bool, error) {
	r, ok := s.resources[name]
	return r, ok, nil
}

func (s *fakeDriveStore) ReserveDriveFolder(_ context.Context, _ uuid.UUID, r GroupResource) (GroupResource, error) {
	s.resources[r.Name] = r
	return r, nil
}

func (s *fakeDriveStore) UpdateResourceResult(_ context.Context, _ uuid.UUID, r GroupResource) error {
	s.resources[r.Name] = r
	return nil
}

type fakeTokenSource struct{}

func (fakeTokenSource) AccessToken(context.Context) (string, error) { return "test-token", nil }

type driveOpRecorder struct{ ops []ExternalOperation }

func (f *driveOpRecorder) RecordExternalOperation(_ context.Context, op ExternalOperation) error {
	f.ops = append(f.ops, op)
	return nil
}

func newDriveService(gw *fakeDriveGateway, store *fakeDriveStore) *DriveService {
	return &DriveService{
		Tokens:     fakeTokenSource{},
		Gateway:    gw,
		Store:      store,
		Operations: &driveOpRecorder{},
	}
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestDriveApplyAccessRulesStaffGetAllFolders(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()

	folders, err := svc.Provision(context.Background(), groupID, "Test Group")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	rules := DriveAccessRules{
		StaffEmails:   []string{"staff1@x.example", "lecturer@x.example"},
		StudentEmails: []string{"student1@x.example", "student2@x.example"},
	}
	if err := svc.ApplyAccessRules(context.Background(), groupID, folders, rules); err != nil {
		t.Fatalf("ApplyAccessRules: %v", err)
	}

	// Staff get writer on ALL 6 folders (root + 5 subfolders).
	for _, email := range rules.StaffEmails {
		totalStaff := 0
		for _, perms := range gw.Permissions {
			for _, p := range perms {
				if p.Email == email && p.Role == "writer" {
					totalStaff++
				}
			}
		}
		if totalStaff != 6 { // root + General + Lectures + Assignments + Resources + Student Data
			t.Errorf("staff %s: %d writer grants, want 6 (all folders)", email, totalStaff)
		}
	}
}

func TestDriveStudentDataNeverGrantedToStudents(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()

	folders, err := svc.Provision(context.Background(), groupID, "Test Group")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	// Find the Student Data folder ID.
	var studentDataID string
	for _, sub := range folders.Subfolders {
		if sub.Name == "Student Data" {
			studentDataID = sub.ID
		}
	}
	if studentDataID == "" {
		t.Fatal("Student Data folder not found")
	}

	rules := DriveAccessRules{
		StaffEmails:   []string{"staff@x.example"},
		StudentEmails: []string{"student@x.example"},
	}
	if err := svc.ApplyAccessRules(context.Background(), groupID, folders, rules); err != nil {
		t.Fatalf("ApplyAccessRules: %v", err)
	}

	// CRITICAL: no student should have ANY permission on Student Data.
	for _, perm := range gw.Permissions[studentDataID] {
		if perm.Email == "student@x.example" {
			t.Errorf("SECURITY: student got %s access to Student Data", perm.Role)
		}
	}

	// Staff DOES have access to Student Data.
	staffHasAccess := false
	for _, perm := range gw.Permissions[studentDataID] {
		if perm.Email == "staff@x.example" && perm.Role == "writer" {
			staffHasAccess = true
		}
	}
	if !staffHasAccess {
		t.Error("staff should have writer on Student Data")
	}
}

func TestDriveStudentsGetReaderOnAccessibleFolders(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()

	folders, _ := svc.Provision(context.Background(), groupID, "Test Group")

	rules := DriveAccessRules{
		StaffEmails:   []string{"staff@x.example"},
		StudentEmails: []string{"student@x.example"},
	}
	if err := svc.ApplyAccessRules(context.Background(), groupID, folders, rules); err != nil {
		t.Fatalf("ApplyAccessRules: %v", err)
	}

	// Student gets reader on exactly 4 folders (General, Lectures, Assignments, Resources).
	studentReaderCount := 0
	for folderID, perms := range gw.Permissions {
		folderName := gw.Folders[folderID]
		for _, p := range perms {
			if p.Email == "student@x.example" && p.Role == "reader" {
				if !driveStudentAccessible[folderName] {
					t.Errorf("student got reader on %s which should be restricted", folderName)
				}
				studentReaderCount++
			}
		}
	}
	if studentReaderCount != 4 {
		t.Errorf("student reader grants = %d, want 4", studentReaderCount)
	}
}

func TestDriveApplyAccessRulesIdempotent(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()

	folders, _ := svc.Provision(context.Background(), groupID, "Test Group")
	rules := DriveAccessRules{
		StaffEmails:   []string{"staff@x.example"},
		StudentEmails: []string{"student@x.example"},
	}

	// Run twice — GrantAccess is idempotent, no errors expected.
	for i := 0; i < 2; i++ {
		if err := svc.ApplyAccessRules(context.Background(), groupID, folders, rules); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

func TestDriveProvisionCreatesCorrectSubfolders(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()

	folders, err := svc.Provision(context.Background(), groupID, "Python Backend 13")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if folders.Root.Name != "Python Backend 13" {
		t.Errorf("root name = %q", folders.Root.Name)
	}
	wantSubs := []string{"General", "Lectures", "Assignments", "Resources", "Student Data"}
	if len(folders.Subfolders) != len(wantSubs) {
		t.Fatalf("subfolders = %d, want %d", len(folders.Subfolders), len(wantSubs))
	}
	for i, want := range wantSubs {
		if folders.Subfolders[i].Name != want {
			t.Errorf("subfolder[%d] = %q, want %q", i, folders.Subfolders[i].Name, want)
		}
	}
}

func TestDrivePermissionFailureDoesNotBlockOtherGroupMembers(t *testing.T) {
	gw, store := newFakeDriveGateway(), newFakeDriveStore()
	svc := newDriveService(gw, store)
	groupID := uuid.New()
	folders, err := svc.Provision(t.Context(), groupID, "Group")
	if err != nil {
		t.Fatal(err)
	}
	gw.Reject = map[fakePerm]bool{
		{Email: "bad-staff@example.test", Role: "writer"}:   true,
		{Email: "bad-student@example.test", Role: "reader"}: true,
	}
	staff := fakePerm{Email: "staff@example.test", Role: "writer"}
	student := fakePerm{Email: "student@example.test", Role: "reader"}
	err = svc.ApplyAccessRules(t.Context(), groupID, folders, DriveAccessRules{
		StaffEmails:   []string{"bad-staff@example.test", staff.Email},
		StudentEmails: []string{"bad-student@example.test", student.Email, " Staff@EXAMPLE.test "},
	})
	if err == nil || !strings.Contains(err.Error(), "10 Drive permission grants failed") {
		t.Fatalf("ApplyAccessRules = %v, want all ten failed grants reported", err)
	}
	if !slices.Equal(gw.Permissions[folders.Root.ID], []fakePerm{staff}) {
		t.Fatalf("root permissions = %v, want staff only", gw.Permissions[folders.Root.ID])
	}
	for _, folder := range folders.Subfolders {
		want := []fakePerm{staff}
		if folder.Name != "Student Data" {
			want = append(want, student)
		}
		if got := gw.Permissions[folder.ID]; !slices.Equal(got, want) {
			t.Errorf("%s permissions = %v, want %v", folder.Name, got, want)
		}
	}
}
