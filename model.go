package main

import "github.com/gorilla/websocket"

type Server struct {
	Rooms map[int]*Room
}

type Room struct {
	ID      int             `json:"id"`
	Players map[int]*Client `json:"players"`
	Game    *Game
}

type Client struct {
	ID     int
	Conn   *websocket.Conn
	RoomID int
}

type Game struct {
	Turn       int
	Bell       Bell
	CardQueues map[int]*CardQueue
	CardStacks map[int]*CardStack
	PlayerIDs  []int
}

type Bell struct {
	IsRinged bool
}
type CardQueue struct {
	Queue []Card
}

func (q *CardQueue) Push(c Card) {
	q.Queue = append(q.Queue, c)
}
func (q *CardQueue) Pop() (Card, bool) {
	if len(q.Queue) == 0 {
		return Card{}, false
	}
	card := q.Queue[0]
	q.Queue = q.Queue[1:]
	return card, true
}

type CardStack struct {
	Stack []Card
}

func (s *CardStack) Push(c Card) {
	s.Stack = append(s.Stack, c)
}
func (s *CardStack) Pop() (Card, bool) {
	if len(s.Stack) == 0 {
		return Card{}, false
	}
	card := s.Stack[len(s.Stack)-1]
	s.Stack = s.Stack[:len(s.Stack)-1]
	return card, true
}
func (s *CardStack) Top() (Card, bool) {
	if len(s.Stack) == 0 {
		return Card{}, false
	}
	return s.Stack[len(s.Stack)-1], true
}

type Card struct {
	ID     int    `json:"id"`
	Fruit  string `json:"fruit"`
	Number int8   `json:"number"`
}

type Packet struct {
	Type   string `json:"type"` // create_room, join_room, ring, draw
	RoomID int    `json:"room_id,omitempty"`
}

//
//✦ 할리갈리 중앙 서버 구현을 완료했습니다. 주요 구현 사항은 다음과 같습니다:
//
//1. 게임 로직 및 2인 플레이 지원:
//* 두 명의 플레이어가 방에 입장하면 자동으로 게임이 시작됩니다.
//* 표준 할리갈리 카드 구성(과일별 1~5개, 총 56장)을 생성하고 셔플하여 분배합니다.
//* draw 액션 시 턴을 교체하고 각 사용자의 카드 덱(Queue)에서 버리는 카드 더미(Stack)로 카드를 이동시킵니다.
//
//2. 카드 가시성(Visibility) 제어:
//* 상대방의 카드 더미를 전송할 때, 가장 최근에 낸 카드(Top card)를 제외한 모든 카드는 is_visible: false로 처리하여 과일 종류와 숫자를 숨깁니다.
//* 본인의 카드 더미는 모든 카드 정보를 확인할 수 있습니다.
//* 카드 덱(Queue)은 앞면이 보이지 않는 상태이므로 남은 카드 개수만 전송합니다.
//
//3. 브로드캐스팅 시스템:
//* draw 및 ring(종 치기) 액션이 발생할 때마다 게임의 전체 상태를 모든 클라이언트에게 브로드캐스트합니다.
//* 상태 전송 시 각 플레이어의 시점에 맞춰 필터링된 정보를 전달합니다.
//
//4. 종 치기(Ring) 및 승리 조건:
//* 종을 쳤을 때 바닥에 깔린 특정 과일의 합계가 정확히 5개인지 확인합니다.
//* 성공 시 바닥의 모든 카드를 획득하며, 실패 시 상대방에게 자신의 카드 한 장을 패널티로 줍니다.
//* 카드 덱과 더미가 모두 소진된 플레이어가 생기면 게임이 종료(game_over)됩니다.
//
//5. 통신 사양:
//* gorilla/websocket을 사용하여 Pygame 클라이언트와 실시간 통신이 가능하도록 구현되었습니다.
//* ws://localhost:8080/ws?player_id=123 형식으로 연결할 수 있습니다.
//
//이제 서버를 실행(go run .)하여 Pygame 클라이언트와 연결해 게임을 진행할 수 있습니다.
//
//[Active Topic: Finalizing Implementation]
//
//
//? for shortcuts
//─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
//auto-accept edits Shift+Tab to plan
//▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
//>
