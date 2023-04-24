package kvraft

//列举错误类型
const (
	OK              = "OK"
	ErrNoKey        = "ErrNoKey"
	ErrWrongLeader  = "ErrWrongLeader"
	ErrShutdown     = "ErrShutdown"
	ErrInitElection = "ErrInitElection"
)

//列举操作类型
const (
	GetOperation    = "Get"
	PutOperation    = "Put"
	AppendOperation = "Append"
)

//错误类型
type Err string

//put|append操作的参数
type PutAppendArgs struct {
	Key      string
	Value    string
	ClientID int64
	OPID     int
	OP       string // "Put" or "Append"
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

//put|append操作的返回值
type PutAppendReply struct {
	Err Err
}

//get操作的返回值
type GetReply struct {
	Err   Err
	Value string
}

//get操作的参数
type GetArgs struct {
	Key      string
	ClientID int64
	OPID     int
}
