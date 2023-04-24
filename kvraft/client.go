package kvraft

import "6.824/labrpc"
import "crypto/rand"
import "math/big"
import "time"
import "log"

//客户结构体
type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.

	me     int64 //clientId，利用随机数生成
	leader int
	opID   int
}

const (
	InitTime = 100
)

//生成随机数作为clientId
func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

//创建CLerk对象
func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// You'll have to add code here.

	ck.me = nrand()
	ck.opID = 1
	ck.leader = 0
	log.Printf("1")
	return ck
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
/*
Get函数
	传入key值
	返回calue值
	位于Clerk结构体下
*/
func (ck *Clerk) Get(key string) string {
	//log.Printf("client : %d  start to get  %T", ck.me,key)
	// get操作的参数
	args := &GetArgs{
		Key:      key,
		ClientID: ck.me,
		OPID:     ck.opID,
	}

	ck.opID++             //下一次操作使用
	serverID := ck.leader //从上一次操作确定的leader开始尝试发送请求
	for {
		reply := &GetReply{}

		//要调用的服务名和方法名（svcMeth）、传递给该方法的参数（args）以及存储返回值的变量（reply）
		ok := ck.servers[serverID].Call("KVServer.Get", args, reply)        //通过rpc调用server的get方法
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrShutdown { //请求的server不是leader或者这个server挂了，则继续请求下一个server
			serverID = (serverID + 1) % len(ck.servers)
			//log.Printf("client : %d  start to get  from %d", ck.me,serverID)
			continue
		}
		if reply.Err == ErrInitElection { //还未选举出leader
			time.Sleep(InitTime * time.Millisecond)
			continue
		}
		ck.leader = serverID       //没有以上两种情况则说明leader是当前的server
		if reply.Err == ErrNoKey { //Get的key并不存在，则返回空值
			return ""
		}
		if reply.Err == OK { //请求成功了，结束循环并返回get的结果
			return reply.Value
		}
	}
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
/*
PutAppend函数
	传入<key,value>和操作类型（Put还是Append）
	位于Clerk结构体下
*/
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	//log.Printf("client : %d  start to putappend  %T", ck.me,key)

	//put，append操作的参数
	args := &PutAppendArgs{
		Key:      key,
		Value:    value,
		ClientID: ck.me,
		OPID:     ck.opID,
		OP:       op,
	}
	ck.opID++             //下一次操作使用
	serverID := ck.leader //从上一次操作确定的leader开始尝试发送请求
	for {
		reply := &PutAppendReply{}
		ok := ck.servers[serverID].Call("KVServer.PutAppend", args, reply) //发送PutAppend的rpc请求
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrShutdown {
			serverID = (serverID + 1) % len(ck.servers)
			//log.Printf("client : %d  finish to put  from %d", ck.me,serverID)

			continue
		}
		if reply.Err == ErrInitElection {
			time.Sleep(InitTime * time.Millisecond)
			continue
		}
		ck.leader = serverID
		if reply.Err == OK {
			return
		}

	}
	//log.Printf("client : %d  finish to putappend  %T", ck.me,key)

}

/*
单独的put操作和append操作都是一样的，直接调用PutAppend操作即可
*/
func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, PutOperation)
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, AppendOperation)
}
