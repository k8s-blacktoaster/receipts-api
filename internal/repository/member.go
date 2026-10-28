package repository

import (
	"context"
	"fmt"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// MemberRepository manages household members.
type MemberRepository interface {
	Create(ctx context.Context, m *models.Member) error
	List(ctx context.Context) ([]models.Member, error)
}

type memberRepo struct{ db *database.DB }

func NewMemberRepository(db *database.DB) MemberRepository { return &memberRepo{db: db} }

func (r *memberRepo) Create(ctx context.Context, m *models.Member) error {
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO members (display_name, email) VALUES ($1, $2) RETURNING id, created_at, updated_at`,
		m.DisplayName, nullStr(m.Email)).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return fmt.Errorf("failed to create member: %w", err)
	}
	return nil
}

func (r *memberRepo) List(ctx context.Context) ([]models.Member, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, display_name, COALESCE(email,''), created_at, updated_at FROM members ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("failed to list members: %w", err)
	}
	defer rows.Close()
	var out []models.Member
	for rows.Next() {
		var m models.Member
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Email, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
