package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"bytes"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const Debug = false
const (
	CheckTermInterval            = 100
	SnapshotThreshold            = 0.9
	SnapshoterCheckInterval      = 100
	SnapshoterAppliedMsgInterval = 50
)

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

// 操作类型
type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Command  string
	Key      string
	Value    string
	ClientID int64
	OPID     int
}

// 返回的信息
type applyResult struct {
	Err   Err
	Value string
	OPID  int
}

// 阻塞等待apply结果
type commandEntry struct {
	op           Op
	replyChannel chan applyResult
}

// 数据库服务类
type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate float64 // 如果log达到这个长度，则需要进行snapshot

	// Your definitions here.
	// 最后一个提交给applyCh的index
	lastApplied int

	// commandndex to commandntry
	//记录所有请求，key为Raft返回的log中记录的操作的index，value为一个chan以阻塞等待apply结果
	commandApply map[int]commandEntry

	// Client to RPC结果
	//每个元素对应一个Client最后请求的结果
	ClientSequence map[int64]applyResult

	// 模拟数据库
	DB map[string]string
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	//log.Printf("server:%d start to get %T", kv.me,args.Key)

	//判断当前服务器是否已经被kill，如果是则返回ErrShutdown。
	if kv.killed() {
		reply.Err = ErrShutdown
		return
	}

	//得到需要进行的操作的参数
	op := Op{
		Command:  GetOperation,
		Key:      args.Key,
		Value:    "",
		ClientID: args.ClientID,
		OPID:     args.OPID,
	}

	kv.mu.Lock()
	//通过Raft协议的Start()方法将该操作添加到Raft Log中
	//获取返回的该次操作的log的index、任期term和是否leader的标志isleader
	index, term, isleader := kv.rf.Start(op)
	//log.Printf("server:%d receive get %s client %d  index %d raft_index %d",kv.me, op.Key,args.ClientID,args.OPID,index)
	if term == 0 {
		//term为0，表示选举还未完成，则返回ErrInitElection。
		kv.mu.Unlock()
		reply.Err = ErrInitElection
		return
	}
	if !isleader {
		//不是leader，则返回ErrWrongLeader。
		kv.mu.Unlock()
		reply.Err = ErrWrongLeader
		return
	}

	//创建一个channel用于接收apply结果
	channel := make(chan applyResult)
	//将channel加入到commandApply[index]中，以便后续apply到state machine(加入数据库)。
	kv.commandApply[index] = commandEntry{
		op:           op,
		replyChannel: channel,
	}
	kv.mu.Unlock()
	//log.Printf("server:%d start to wait get %s  last %d,client %d  Index %d raft_index %d", kv.me,args.Key,index,args.ClientID,args.OPID,index)

	//该标签与判断kv是否死亡的for循环绑定
CheckTerm:
	for !kv.killed() {
		select {
		case result, ok := <-channel:
			//成功接收到apply结果
			if !ok {
				reply.Err = ErrShutdown
				return
			}
			reply.Err = result.Err
			reply.Value = result.Value
			//log.Printf("server:%d finish Get %s", kv.me,args.Key)
			return
		case <-time.After(CheckTermInterval * time.Millisecond):
			//等待超时
			tempTerm, isLeader := kv.rf.GetState()
			if tempTerm != term || !isLeader {
				//切换任期了导致leader发生了变化
				reply.Err = ErrWrongLeader
				break CheckTerm //直接结束for循环
			}
			//log.Printf("server:%d still wait get %s  last %d,client %d  Index %d", kv.me,args.Key,index,args.ClientID,args.OPID)
		}
	}

	//leader错误后才会执行到这里，将后续的消息都接受并直接丢弃
	go func() { <-channel }()

	if kv.killed() {
		reply.Err = ErrShutdown
		return
	}
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	//log.Printf("server:%d start to PutAppend %T", kv.me, args.Key)

	if kv.killed() {
		reply.Err = ErrShutdown
		return
	}

	op := Op{
		Command:  args.OP,
		Key:      args.Key,
		Value:    args.Value,
		ClientID: args.ClientID,
		OPID:     args.OPID,
	}

	kv.mu.Lock()
	index, term, isleader := kv.rf.Start(op)
	//log.Printf("server:%d receive  put %s  client %d  index %d raft_index %d", kv.me, op.Key, args.ClientID, args.OPID, index)
	//log.Println(op)
	if term == 0 {
		kv.mu.Unlock()
		reply.Err = ErrInitElection
		return
	}
	if !isleader {
		kv.mu.Unlock()
		reply.Err = ErrWrongLeader
		return
	}

	channel := make(chan applyResult)
	kv.commandApply[index] = commandEntry{
		op:           op,
		replyChannel: channel,
	}
	kv.mu.Unlock()
	timeout := 0
	//log.Printf("server:%d start to wait put %s,%s ,last %d,client %d  Index %d", kv.me, args.Key, args.Value, index, args.ClientID, args.OPID)

CheckTerm:

	for !kv.killed() {
		select {
		case result, ok := <-channel:
			if !ok {
				reply.Err = ErrShutdown
				return
			}
			reply.Err = result.Err
			//log.Printf("server:%d finish put %s %s ", kv.me, args.Key, args.Value)
			return
		case <-time.After(CheckTermInterval * time.Millisecond):
			tempTerm, isLeader := kv.rf.GetState()
			timeout++
			if tempTerm != term || !isLeader {
				reply.Err = ErrWrongLeader
				break CheckTerm
			}
			//log.Printf("server:%d still wait put %s,%s ,last %d,client %d  Index %d", kv.me, args.Key, args.Value, index, args.ClientID, args.OPID)

		}
	}

	go func() { <-channel }()

	if kv.killed() {
		reply.Err = ErrShutdown
		return
	}

}

func (kv *KVServer) apply(applyCh <-chan raft.ApplyMsg, lastSnapshoterTriggeredCommandIndex int, snapshotTriger chan<- bool) {

	var result string

	for message := range applyCh {

		//如果消息携带的是快照数据，则使用快照数据更新 KVServer 的状态，并清除所有未完成的命令请求。
		if message.SnapshotValid {
			kv.mu.Lock()
			kv.lastApplied = message.SnapshotIndex
			//log.Printf("server:%d start send snapshot last apply %d", kv.me, kv.lastApplied)
			//log.Println(kv.DB, kv.lastApplied, kv.me)

			//调用 readSnapShot 函数更新 KVServer 的状态
			kv.readSnapShot(message.Snapshot)

			//清除所有的reply channel，以避免 goroutine 资源泄漏
			for _, ca := range kv.commandApply {
				ca.replyChannel <- applyResult{Err: ErrWrongLeader}
				//log.Printf("server:%d forgive %s", kv.me, ca.op.Key)
			}
			kv.commandApply = make(map[int]commandEntry)
			//log.Printf("server:%d finish send snapshot last apply %d", kv.me, kv.lastApplied)
			//log.Println(kv.DB, kv.lastApplied, kv.me)
			kv.mu.Unlock()
			continue
		}

		//如果消息携带的是命令请求，则按照命令类型执行相应的操作，更新 KVServer 的状态，并向客户端返回操作结果。
		if !message.CommandValid {
			continue
		}
		if message.CommandIndex-lastSnapshoterTriggeredCommandIndex > SnapshoterAppliedMsgInterval {
			select {
			case snapshotTriger <- true:
				lastSnapshoterTriggeredCommandIndex = message.CommandIndex
			default:
			}

		}
		//得到本次操作的信息
		op := message.Command.(Op)
		//log.Printf("server:%d  apply %s  receive with client %d index %d", kv.me, op.Key, op.ClientID, op.OPID)
		kv.mu.Lock()
		//更新操作编号
		kv.lastApplied = message.CommandIndex
		//进行本次操作的client的最后一次操作的结果
		lastResult, ok := kv.ClientSequence[op.ClientID]

		//判断该操作是否已经执行过，执行过了则直接返回上次执行的结果
		if lastResult.OPID >= op.OPID {
			result = lastResult.Value
			//log.Printf("server:%d use old value ", kv.me)
			//log.Println(kv.ClientSequence[op.ClientID])
			//log.Println(lastResult)
		} else { //没执行过则正常执行
			switch op.Command {
			case GetOperation:
				result = kv.DB[op.Key]
			case PutOperation:
				kv.DB[op.Key] = op.Value
				result = ""
			case AppendOperation:
				kv.DB[op.Key] = kv.DB[op.Key] + op.Value
				result = ""
			}
			kv.ClientSequence[op.ClientID] = applyResult{Value: result, OPID: op.OPID}
			//log.Printf("server:%d add new value value %s  receive with client %d index %d", kv.me, op.Key, op.ClientID, op.OPID)
			//log.Println(kv.ClientSequence[op.ClientID])
			//log.Println(lastResult.Value, "  1   ", lastResult.OPID, op.Command, op.Value)
			//log.Println(kv.DB[op.Key])
		}
		//已经执行过了，需要删除指令
		lastCommand, ok := kv.commandApply[message.CommandIndex]
		if ok {
			delete(kv.commandApply, message.CommandIndex)
			kv.mu.Unlock()
			if lastCommand.op != op {
				lastCommand.replyChannel <- applyResult{Err: ErrWrongLeader}
				//log.Printf("server:%d finish apply but fail %s with client %d index %d", kv.me, op.Key, op.ClientID, op.OPID)

			} else {
				lastCommand.replyChannel <- applyResult{Err: OK, Value: result, OPID: op.OPID}
				//log.Printf("server:%d finish apply and apply %s with client %d index %d", kv.me, op.Key, op.ClientID, op.OPID)

			}
		} else {
			//log.Printf("server:%d hanv no client %d index %d", kv.me, op.ClientID, op.OPID)
			kv.mu.Unlock()
		}
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	for _, ce := range kv.commandApply {
		close(ce.replyChannel)
	}
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
// 用于启动 KVServer。返回一个新的 KVServer 实例。
// servers []*labrpc.ClientEnd：一个指向所有服务器端点的切片。
// me int：当前服务器的 ID。
// persister *raft.Persister：一个 Raft 持久化实例，用于保存和恢复 Raft 快照和日志。
// maxraftstate int：Raft 日志的最大大小，以字节为单位。
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = float64(maxraftstate)

	// You may need initialization code here.

	//绑定raft实例
	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	kv.lastApplied = kv.rf.LastIncludedIndex
	kv.commandApply = make(map[int]commandEntry)
	kv.ClientSequence = make(map[int64]applyResult)
	kv.DB = make(map[string]string)

	kv.readSnapShot(persister.ReadSnapshot())

	snapshotTriger := make(chan bool, 1)
	log.Printf("server:%d start ", kv.me)
	log.Println(kv.DB, kv.me, kv.lastApplied)
	go kv.apply(kv.applyCh, kv.lastApplied, snapshotTriger)
	go kv.snapshot(persister, snapshotTriger)

	return kv
}

// 该方法的目的是在满足一定条件时，对当前的键值对数据进行快照
func (kv *KVServer) snapshot(persister *raft.Persister, snapshotTriger <-chan bool) {
	//这里的kv.maxraftstate是一个表示Raft状态最大值的变量，如果其值小于0，说明不需要进行快照操作
	if kv.maxraftstate < 0 {
		return
	}
	for !kv.killed() {
		ratio := float64(persister.RaftStateSize()) / kv.maxraftstate
		//在进入无限循环之前，首先计算当前Raft状态的大小与最大值的比率，如果比率超过了阈值（SnapshotThreshold），则进行快照操作
		if ratio > SnapshotThreshold {
			kv.mu.Lock()
			//在进行快照之前，需要先获取kvServerSnapShot函数返回的键值对数据，如果返回为空，则输出错误信息。否则，输出快照信息，并调用Snapshot方法对当前状态进行快照操作
			if data := kv.kvServerSnapShot(); data == nil {
				log.Printf("server :%d has some thing wrong in persist", kv.me)
			} else {
				log.Printf("server :%d snapshot", kv.me)
				log.Println(kv.DB, kv.me, kv.lastApplied)
				kv.rf.Snapshot(kv.lastApplied, data)
			}
			//将比率重置为0，释放锁
			ratio = 0.0
			kv.mu.Unlock()
		}
		//通过select语句来检查是否有快照触发信号
		select {
		//如果有，则立即进行快照操作
		case <-snapshotTriger:
		//如果没有，则通过time.After方法来等待一定时间（时间间隔根据当前状态大小和最大值的比率来动态调整），然后再次进入循环
		//这里的SnapshoterCheckInterval表示两次检查之间的时间间隔，单位为毫秒。循环会一直进行，直到kv.killed()返回true为止，即该节点被杀死
		case <-time.After(time.Duration((1-ratio)*SnapshoterCheckInterval) * time.Millisecond):
		}
	}
}

func (kv *KVServer) kvServerSnapShot() []byte {
	//创建一个新的 bytes.Buffer 缓冲区，将会用于编码结构体成员
	w := new(bytes.Buffer)
	//创建一个 Encoder 对象，用于将结构体成员编码到 bytes.Buffer 缓冲区中
	e := labgob.NewEncoder(w)
	//使用 Encoder 对象将 kv.DB 和 kv.ClientSequence 成员进行编码。如果在编码过程中出现错误，返回 nil
	if e.Encode(kv.DB) != nil ||
		e.Encode(kv.ClientSequence) != nil {
		return nil
	}
	//将 bytes.Buffer 缓冲区中的内容转化为字节数组，并返回
	return w.Bytes()
}

func (kv *KVServer) readSnapShot(data []byte) {
	//如果传入的字节数组长度为0，则直接返回
	if len(data) == 0 {
		return
	}
	//创建一个字节缓冲区r，并将传入的字节数组data写入缓冲区
	r := bytes.NewBuffer(data)
	//创建一个labgob包的解码器d，用于将字节流解码为Go语言的类型
	d := labgob.NewDecoder(r)
	//声明两个变量db和cs，db是一个字符串到字符串的映射，cs是一个int64到applyResult的映射
	var db map[string]string
	var cs map[int64]applyResult
	//使用Decode方法将字节流解码为变量db和cs。如果解码失败，则直接返回
	if d.Decode(&db) != nil ||
		d.Decode(&cs) != nil {
		return
	}
	//将解码后的db和cs分别赋值给kv结构体的成员变量DB和ClientSequence
	kv.DB = db
	kv.ClientSequence = cs
	//打印一条日志，表示读取快照成功，并输出DB、cs以及该服务器的IDkv.me
	log.Printf("server :%d read snapshot", kv.me)
	log.Println(kv.DB, cs, kv.me)
}
