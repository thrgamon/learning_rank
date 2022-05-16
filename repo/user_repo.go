package repo

import (
	"context"

	"github.com/jackc/pgx/v4/pgxpool"
)

type UserID uint

type User struct {
	ID       UserID
	Username string
	AuthId   string
}

type UserRepo struct {
	db *pgxpool.Pool
}

func NewUserRepo(db *pgxpool.Pool) *UserRepo {
	var repo UserRepo
	repo.db = db
	return &repo
}

func (rr UserRepo) Get(ctx context.Context, authId string) (User, error) {
	var userId UserID
	var username string
	err := rr.db.QueryRow(context.TODO(), "select id, username from users where users.auth_id = $1", authId).Scan(&userId, &username)

	if err != nil {
		return User{}, err
	}

	user := User{ID: userId, Username: username, AuthId: authId}

	return user, nil
}

func (rr UserRepo) Exists(ctx context.Context, authId string) (bool, error) {
	var exists bool
	err := rr.db.QueryRow(ctx, "SELECT EXISTS(select 1 from users where auth_id=$1)", authId).Scan(&exists)

	return exists, err
}

func (rr UserRepo) Add(ctx context.Context, username string, authId string) error {
	_, err := rr.db.Exec(ctx, "INSERT INTO users (username, auth_id) VALUES ($1, $2)", username, authId)

	return err
}
