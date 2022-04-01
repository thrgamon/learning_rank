package repo

import (
	"context"

	"github.com/jackc/pgx/v4/pgxpool"
)

type User struct {
  ID uint
  Username string
  AuthId string
}

type UserRepo struct {
  db *pgxpool.Pool
}

func NewUserRepo(db *pgxpool.Pool) *UserRepo{
  var repo UserRepo
  repo.db = db
  return &repo
}

func (rr UserRepo) Get(ctx context.Context, id uint) (error, User){
  var userId uint
  var username string
  var authId string
  err := rr.db.QueryRow(context.TODO(), "select id, username, auth_id from users where users.id = $1", id).Scan(&userId, &username, &authId)

  if err != nil {
    return err, User{}
  }

  user := User{ID: id, Username: username, AuthId: authId}

  return nil, user
}

func (rr UserRepo) Exists(ctx context.Context, authId string) (error, bool) {
  var exists bool
  err := rr.db.QueryRow(ctx, "SELECT EXISTS(select 1 from users where auth_id=$1)", authId).Scan(&exists)

  return err, exists
}

func (rr UserRepo) Add(ctx context.Context, username string, authId string) error {
  _, err := rr.db.Exec(ctx, "INSERT INTO users (username, auth_id) VALUES ($1, $2)", username, authId)

  return err
}