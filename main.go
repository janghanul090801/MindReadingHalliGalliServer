package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	server = Server{
		Rooms: make(map[int]*Room),
	}
	serverMu   sync.Mutex
	nextRoomID = 1
)

func main() {
	rand.Seed(time.Now().UnixNano())
	http.HandleFunc("/ws", wsHandler)
	fmt.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	playerIDStr := r.URL.Query().Get("player_id")
	if playerIDStr == "" {
		http.Error(w, "player id is required", http.StatusBadRequest)
		return
	}

	playerID, err := strconv.Atoi(playerIDStr)
	if err != nil {
		http.Error(w, "player id is invalid", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &Client{
		Conn: conn,
		ID:   playerID,
	}

	defer func() {
		RemoveClient(client)
		conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var packet Packet
		if err := json.Unmarshal(data, &packet); err != nil {
			log.Println("Unmarshal error:", err)
			continue
		}

		switch packet.Type {
		case "create_room":
			HandleCreateRoom(client)
		case "join_room":
			HandleJoinRoom(client, packet.RoomID)
		case "ring":
			HandleRing(client)
		case "draw":
			HandleDraw(client)
		default:
			log.Println("unknown packet:", packet.Type)
		}
	}
}

func RemoveClient(client *Client) {
	serverMu.Lock()
	defer serverMu.Unlock()
	if room, ok := server.Rooms[client.RoomID]; ok {
		delete(room.Players, client.ID)
		if len(room.Players) == 0 {
			delete(server.Rooms, client.RoomID)
		}
	}
}

func HandleCreateRoom(client *Client) {
	serverMu.Lock()
	roomID := nextRoomID
	nextRoomID++
	room := &Room{
		ID:      roomID,
		Players: make(map[int]*Client),
	}
	server.Rooms[roomID] = room
	serverMu.Unlock()

	room.Players[client.ID] = client
	client.RoomID = room.ID

	Send(client, map[string]any{
		"type":    "room_created",
		"room_id": room.ID,
	})
}

func HandleJoinRoom(client *Client, roomID int) {
	serverMu.Lock()
	room, ok := server.Rooms[roomID]
	serverMu.Unlock()

	if !ok {
		Send(client, map[string]any{"type": "error", "message": "room not found"})
		return
	}

	room.Players[client.ID] = client
	client.RoomID = roomID

	if len(room.Players) == 2 {
		StartGame(room)
	}
}

func StartGame(room *Room) {
	playerIDs := make([]int, 0, 2)
	for id := range room.Players {
		playerIDs = append(playerIDs, id)
	}

	game := &Game{
		Turn:       playerIDs[0],
		PlayerIDs:  playerIDs,
		CardQueues: make(map[int]*CardQueue),
		CardStacks: make(map[int]*CardStack),
	}

	// Create and shuffle deck
	fruits := []string{"strawberry", "banana", "lime", "plum"}
	cards := make([]Card, 0)
	id := 1
	for _, fruit := range fruits {
		// Halli Galli standard: 5 cards of 1, 3 of 2, 3 of 3, 2 of 4, 1 of 5 per fruit
		// Simplified for now: 13 cards per fruit as standard
		counts := []int{5, 3, 3, 2, 1}
		for i, count := range counts {
			for j := 0; j < count; j++ {
				cards = append(cards, Card{ID: id, Fruit: fruit, Number: int8(i + 1)})
				id++
			}
		}
	}
	rand.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })

	// Distribute cards
	game.CardQueues[playerIDs[0]] = &CardQueue{Queue: cards[:len(cards)/2]}
	game.CardQueues[playerIDs[1]] = &CardQueue{Queue: cards[len(cards)/2:]}
	game.CardStacks[playerIDs[0]] = &CardStack{Stack: make([]Card, 0)}
	game.CardStacks[playerIDs[1]] = &CardStack{Stack: make([]Card, 0)}

	room.Game = game
	BroadcastGameState(room)
}

func HandleDraw(client *Client) {
	serverMu.Lock()
	room, ok := server.Rooms[client.RoomID]
	serverMu.Unlock()

	if !ok || room.Game == nil {
		return
	}

	if room.Game.Turn != client.ID {
		return
	}

	card, ok := room.Game.CardQueues[client.ID].Pop()
	if !ok {
		// Player lost or something, but let's just return for now
		return
	}

	room.Game.CardStacks[client.ID].Push(card)

	// Switch turn
	for _, id := range room.Game.PlayerIDs {
		if id != client.ID {
			room.Game.Turn = id
			break
		}
	}

	BroadcastGameState(room)
}

func HandleRing(client *Client) {
	serverMu.Lock()
	room, ok := server.Rooms[client.RoomID]
	serverMu.Unlock()

	if !ok || room.Game == nil {
		return
	}

	// Calculate total fruits visible
	fruitCounts := make(map[string]int)
	for _, stack := range room.Game.CardStacks {
		if top, ok := stack.Top(); ok {
			fruitCounts[top.Fruit] += int(top.Number)
		}
	}

	isFive := false
	for _, count := range fruitCounts {
		if count == 5 {
			isFive = true
			break
		}
	}

	if isFive {
		// Winner takes all stacks
		winnerID := client.ID
		for _, stack := range room.Game.CardStacks {
			for len(stack.Stack) > 0 {
				card, _ := stack.Pop()
				room.Game.CardQueues[winnerID].Push(card)
			}
		}
		Broadcast(room, map[string]any{
			"type":      "ring_success",
			"player_id": winnerID,
		})

		// Check if opponent has any cards left (in queue or stack)
		var opponentID int
		for _, id := range room.Game.PlayerIDs {
			if id != winnerID {
				opponentID = id
				break
			}
		}
		if len(room.Game.CardQueues[opponentID].Queue) == 0 && len(room.Game.CardStacks[opponentID].Stack) == 0 {
			Broadcast(room, map[string]any{
				"type":      "game_over",
				"winner_id": winnerID,
			})
		}

	} else {
		// Penalty: give one card to opponent
		penaltyID := client.ID
		var opponentID int
		for _, id := range room.Game.PlayerIDs {
			if id != penaltyID {
				opponentID = id
				break
			}
		}

		if card, ok := room.Game.CardQueues[penaltyID].Pop(); ok {
			room.Game.CardQueues[opponentID].Push(card)
		}
		Broadcast(room, map[string]any{
			"type":      "ring_fail",
			"player_id": penaltyID,
		})
	}

	BroadcastGameState(room)
}

func Send(client *Client, v any) {
	data, _ := json.Marshal(v)
	client.Conn.WriteMessage(websocket.TextMessage, data)
}

func Broadcast(room *Room, v any) {
	data, _ := json.Marshal(v)
	for _, player := range room.Players {
		player.Conn.WriteMessage(websocket.TextMessage, data)
	}
}

func BroadcastGameState(room *Room) {
	for _, client := range room.Players {
		state := GetFilteredState(room, client.ID)
		Send(client, state)
	}
}

func GetFilteredState(room *Room, playerID int) map[string]any {
	game := room.Game
	queues := make(map[int]int) // Only send counts for queues
	for id, q := range game.CardQueues {
		queues[id] = len(q.Queue)
	}

	stacks := make(map[int][]any)
	for id, s := range game.CardStacks {
		stackCards := make([]any, 0)
		for i, card := range s.Stack {
			isVisible := true
			// Requirement: "상대의 첫번째 카드를 제외한 모든 카드는 is visible 값을 false 로 해야함"
			// Assuming "first card" of opponent's stack is the TOP card (last in slice)
			if id != playerID {
				if i != len(s.Stack)-1 {
					isVisible = false
				}
			}

			if isVisible {
				stackCards = append(stackCards, card)
			} else {
				stackCards = append(stackCards, map[string]any{
					"id":         card.ID,
					"is_visible": false,
				})
			}
		}
		stacks[id] = stackCards
	}

	return map[string]any{
		"type":        "game_state",
		"turn":        game.Turn,
		"card_queues": queues,
		"card_stacks": stacks,
	}
}
