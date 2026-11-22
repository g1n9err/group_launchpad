package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/groups"
	"launchpad/internal/store/gen"
)

// UpdateGroupRoster replaces only the lists explicitly supplied by the caller.
// A non-nil empty list deliberately clears that role from the group.
func (r *Repo) UpdateGroupRoster(ctx context.Context, groupID uuid.UUID, update groups.RosterUpdate) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, "SELECT status FROM groups WHERE id=$1 FOR UPDATE", groupID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotFound("group", groupID.String())
	}
	if err != nil {
		return err
	}
	if status == "archived" {
		return &core.AppError{Code: core.CodeConflict, Message: "an archived group's roster cannot be changed"}
	}
	q := r.q.WithTx(tx)
	if update.Students != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM group_members gm USING users u WHERE gm.group_id=$1 AND gm.user_id=u.id AND gm.member_role='student' AND NOT (lower(btrim(u.email))=ANY($2))`, groupID, update.Students); err != nil {
			return err
		}
		for _, email := range update.Students {
			id, e := upsertStudentUser(ctx, q, email)
			if e != nil {
				return e
			}
			if _, e = q.InsertGroupMember(ctx, gen.InsertGroupMemberParams{GroupID: groupID, UserID: id}); e != nil {
				return e
			}
		}
	}
	if update.Lecturers != nil || update.TAs != nil {
		var lecturers, tas []string
		if update.Lecturers == nil {
			lecturers, err = rosterEmails(ctx, tx, groupID, "lecturer")
			if err != nil {
				return err
			}
		} else {
			lecturers = update.Lecturers
		}
		if update.TAs == nil {
			tas, err = rosterEmails(ctx, tx, groupID, "ta")
			if err != nil {
				return err
			}
		} else {
			tas = update.TAs
		}
		allStaff := append(append([]string{}, lecturers...), tas...)
		if _, err = tx.Exec(ctx, `DELETE FROM staff_assignments WHERE group_id=$1 AND NOT (lower(btrim(email))=ANY($2))`, groupID, allStaff); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM staff_assignments WHERE group_id=$1 AND role='teaching_staff'`, groupID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM group_members gm USING users u WHERE gm.group_id=$1 AND gm.user_id=u.id AND gm.member_role IN ('teaching_staff','lecturer','ta') AND NOT (lower(btrim(u.email))=ANY($2))`, groupID, allStaff); err != nil {
			return err
		}
		for _, email := range lecturers {
			if _, err = tx.Exec(ctx, `INSERT INTO staff_assignments(group_id,email,role) VALUES($1,$2,'lecturer') ON CONFLICT(group_id,email) DO UPDATE SET role='lecturer'`, groupID, email); err != nil {
				return err
			}
		}
		for _, email := range tas {
			if _, err = tx.Exec(ctx, `INSERT INTO staff_assignments(group_id,email,role) VALUES($1,$2,'ta') ON CONFLICT(group_id,email) DO UPDATE SET role='ta'`, groupID, email); err != nil {
				return err
			}
		}
		for _, roleSet := range []struct {
			role   string
			emails []string
		}{{"lecturer", lecturers}, {"ta", tas}} {
			for _, email := range roleSet.emails {
				var userID uuid.UUID
				e := tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(btrim(email))=$1`, email).Scan(&userID)
				if errors.Is(e, pgx.ErrNoRows) {
					continue
				}
				if e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `UPDATE group_members SET member_role=$3 WHERE group_id=$1 AND user_id=$2 AND member_role IN ('teaching_staff','lecturer','ta')`, groupID, userID, roleSet.role); e != nil {
					return e
				}
			}
		}
	}
	if update.Students != nil || update.Lecturers != nil || update.TAs != nil {
		if _, err = tx.Exec(ctx, `UPDATE classroom_join_deliveries d SET status='cancelled'
			WHERE d.group_id=$1 AND d.status='pending' AND NOT EXISTS (
				SELECT 1 FROM group_members gm JOIN users u ON u.id=gm.user_id
				WHERE gm.group_id=d.group_id AND lower(btrim(u.email))=d.recipient
				UNION ALL
				SELECT 1 FROM staff_assignments sa WHERE sa.group_id=d.group_id AND lower(btrim(sa.email))=d.recipient
			)`, groupID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func rosterEmails(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, role string) ([]string, error) {
	roles := []string{role}
	if role == "ta" {
		roles = []string{"ta", "teaching_staff"}
	}
	query := `SELECT lower(btrim(email)) FROM staff_assignments WHERE group_id=$1 AND role=ANY($2) UNION SELECT lower(btrim(u.email)) FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.group_id=$1 AND gm.member_role=ANY($2)`
	rows, err := tx.Query(ctx, query, groupID, roles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}
