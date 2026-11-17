package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tarefa struct {
	ID     int    `json:"id"`
	Titulo string `json:"titulo"`
	Feita  bool   `json:"feita"`
}

// Store agora guarda o pool de conexões com o Postgres.
// O pool já é seguro para uso concorrente
type Store struct {
	db *pgxpool.Pool
}

func escreverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func erro(w http.ResponseWriter, status int, msg string) {
	escreverJSON(w, status, map[string]string{"erro": msg})
}

// erroInterno loga o erro real no servidor e devolve uma mensagem genérica ao cliente.
func erroInterno(w http.ResponseWriter, err error) {
	log.Println("erro interno:", err)
	erro(w, http.StatusInternalServerError, "erro interno")
}

// READ (lista)
func (s *Store) listar(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(),
		"SELECT id, titulo, feita FROM tarefas ORDER BY id")
	if err != nil {
		erroInterno(w, err)
		return
	}
	defer rows.Close()

	lista := []Tarefa{}
	for rows.Next() {
		var t Tarefa
		if err := rows.Scan(&t.ID, &t.Titulo, &t.Feita); err != nil {
			erroInterno(w, err)
			return
		}
		lista = append(lista, t)
	}
	if err := rows.Err(); err != nil {
		erroInterno(w, err)
		return
	}
	escreverJSON(w, http.StatusOK, lista)
}

// READ (um item)
func (s *Store) buscar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		erro(w, http.StatusBadRequest, "id inválido")
		return
	}

	var t Tarefa
	err = s.db.QueryRow(r.Context(),
		"SELECT id, titulo, feita FROM tarefas WHERE id = $1", id).
		Scan(&t.ID, &t.Titulo, &t.Feita)
	if errors.Is(err, pgx.ErrNoRows) {
		erro(w, http.StatusNotFound, "tarefa não encontrada")
		return
	}
	if err != nil {
		erroInterno(w, err)
		return
	}
	escreverJSON(w, http.StatusOK, t)
}

// CREATE
func (s *Store) criar(w http.ResponseWriter, r *http.Request) {
	var t Tarefa
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		erro(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if t.Titulo == "" {
		erro(w, http.StatusBadRequest, "titulo é obrigatório")
		return
	}

	// RETURNING devolve o id gerado pelo banco
	err := s.db.QueryRow(r.Context(),
		"INSERT INTO tarefas (titulo, feita) VALUES ($1, $2) RETURNING id",
		t.Titulo, t.Feita).Scan(&t.ID)
	if err != nil {
		erroInterno(w, err)
		return
	}
	escreverJSON(w, http.StatusCreated, t)
}

// UPDATE
func (s *Store) atualizar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		erro(w, http.StatusBadRequest, "id inválido")
		return
	}

	var t Tarefa
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		erro(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if t.Titulo == "" {
		erro(w, http.StatusBadRequest, "titulo é obrigatório")
		return
	}

	res, err := s.db.Exec(r.Context(),
		"UPDATE tarefas SET titulo = $1, feita = $2 WHERE id = $3",
		t.Titulo, t.Feita, id)
	if err != nil {
		erroInterno(w, err)
		return
	}
	if res.RowsAffected() == 0 {
		erro(w, http.StatusNotFound, "tarefa não encontrada")
		return
	}
	t.ID = id
	escreverJSON(w, http.StatusOK, t)
}

// DELETE
func (s *Store) deletar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		erro(w, http.StatusBadRequest, "id inválido")
		return
	}

	res, err := s.db.Exec(r.Context(), "DELETE FROM tarefas WHERE id = $1", id)
	if err != nil {
		erroInterno(w, err)
		return
	}
	if res.RowsAffected() == 0 {
		erro(w, http.StatusNotFound, "tarefa não encontrada")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	// A string de conexão vem de uma variável de ambiente (padrão: banco local do Docker)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/crud"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal("erro ao criar pool: ", err)
	}
	defer pool.Close()

	// Ping confirma que a conexão realmente funciona
	if err := pool.Ping(ctx); err != nil {
		log.Fatal("não consegui conectar no Postgres: ", err)
	}

	// Cria a tabela se ainda não existir (para o primeiro CRUD; depois migrations)
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS tarefas (
			id     SERIAL PRIMARY KEY,
			titulo TEXT NOT NULL,
			feita  BOOLEAN NOT NULL DEFAULT false
		)`)
	if err != nil {
		log.Fatal("erro ao criar tabela: ", err)
	}

	s := &Store{db: pool}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /tarefas", s.listar)
	mux.HandleFunc("GET /tarefas/{id}", s.buscar)
	mux.HandleFunc("POST /tarefas", s.criar)
	mux.HandleFunc("PUT /tarefas/{id}", s.atualizar)
	mux.HandleFunc("DELETE /tarefas/{id}", s.deletar)

	log.Println("servidor rodando em http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
