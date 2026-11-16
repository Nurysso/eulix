package main

import "fmt"

type Store struct{}

func (s *Store) Save() { s.load() }
func (s *Store) load() {}

func helper() {}

func main() {
	m := &Store{}
	m.Save()
	helper()
}

type Saver interface{ Save() }

func persist(s Saver) {
	s.Save()
	fmt.Println("saved")
}
