package worker

import (
	"fmt"
	"github.com/yenkeia/cron/common"
	"time"
)

// 任务调度
type Scheduler struct {
	jobEventChan      chan *common.JobEvent              // etcd 任务事件队列
	jobPlanTable      map[string]*common.JobSchedulePlan // 任务调度计划表
	jobExecutingTable map[string]*common.JobExecuteInfo  // 任务执行表
	jobResultChan     chan *common.JobExecuteResult      // 任务结果队列
}

var G_scheduler *Scheduler

// 处理任务事件
func (scheduler *Scheduler) handleJobEvent(jobEvent *common.JobEvent) {
	var (
		jobSchedulePlan *common.JobSchedulePlan
		jobExisted      bool
		err             error
		jobExecuteInfo  *common.JobExecuteInfo
		jobExecuting    bool
	)
	//TODO: 这里可以优化?，即每次修改 jobPlanTable 后，重新计算调度时间 schedule.TrySchedule()
	switch jobEvent.EventType {
	case common.JOB_EVENT_SAVE: // 保存任务事件
		if jobSchedulePlan, err = common.BuildJobSchedulePlan(jobEvent.Job); err != nil {
			return
		}
		scheduler.jobPlanTable[jobEvent.Job.Name] = jobSchedulePlan
	case common.JOB_EVENT_DELETE: // 删除任务事件
		if jobSchedulePlan, jobExisted = scheduler.jobPlanTable[jobEvent.Job.Name]; jobExisted {
			delete(scheduler.jobPlanTable, jobEvent.Job.Name)
		}
	case common.JOB_EVENT_KILL: // 强杀任务事件
		// 取消 Command 执行
		if jobExecuteInfo, jobExecuting = scheduler.jobExecutingTable[jobEvent.Job.Name]; jobExecuting {
			jobExecuteInfo.CancelFunc() // 触发 command 杀死 shell 子进程，任务得到退出
		}
	}
}

// 尝试执行任务
func (scheduler Scheduler) TryStartJob(jobPlan *common.JobSchedulePlan) {
	// 调度和执行是2件事
	// 执行的任务可能运行很久，但是1分钟会调度很多次，只能执行一次
	var (
		jobExecuteInfo *common.JobExecuteInfo
		jobExecuting   bool
	)

	// 如果任务正在执行，跳过本次调度
	if jobExecuteInfo, jobExecuting = scheduler.jobExecutingTable[jobPlan.Job.Name]; jobExecuting {
		fmt.Println("尚未退出，跳过执行: ", jobExecuteInfo.Job.Name)
		return
	}
	// 构建执行状态信息
	jobExecuteInfo = common.BuildJobExecuteInfo(jobPlan)
	// 保存执行状态
	scheduler.jobExecutingTable[jobPlan.Job.Name] = jobExecuteInfo
	// 执行任务
	fmt.Println("尝试执行任务:", jobExecuteInfo.Job.Name, " 计划开始时间:", jobExecuteInfo.PlanTime, " 实际开始时间:", jobExecuteInfo.RealTime)
	G_executor.ExecuteJob(jobExecuteInfo)
}

// 重新计算任务调度状态
// 1. 遍历所有任务
// 2. 过期的任务立即执行
// 3. 统计最近要过期的任务的时间(N秒后过期)，则 sleep N 秒节省 CPU, scheduleAfter = N
func (scheduler *Scheduler) TrySchedule() (scheduleAfter time.Duration) {
	var (
		jobPlan  *common.JobSchedulePlan
		now      time.Time
		nearTime *time.Time
	)
	// 如果任务表为空，随意睡眠
	if len(scheduler.jobPlanTable) == 0 {
		scheduleAfter = 1 * time.Second
		return
	}
	// 当前时间
	now = time.Now()
	// 遍历所有任务
	for _, jobPlan = range scheduler.jobPlanTable {
		if jobPlan.NextTime.Before(now) || jobPlan.NextTime.Equal(now) {
			scheduler.TryStartJob(jobPlan)
			jobPlan.NextTime = jobPlan.Expr.Next(now) // 更新下次执行时间
		}
		// 统计最近一个要过期的任务时间
		if nearTime == nil || jobPlan.NextTime.Before(*nearTime) {
			nearTime = &jobPlan.NextTime
		}
	}
	// 下次调度间隔，即睡眠时间	(最近要执行的任务调度时间 - 当前时间)
	scheduleAfter = (*nearTime).Sub(now)
	return
}

// 处理任务结果
func (scheduler Scheduler) handleJobResult(jobResult *common.JobExecuteResult) {
	var (
		jobLog *common.JobLog
	)
	// 删除 jobExecutingTable 中执行状态
	delete(scheduler.jobExecutingTable, jobResult.ExecuteInfo.Job.Name)
	fmt.Println("		任务执行完成:", jobResult.ExecuteInfo.Job.Name)
	fmt.Println("		Output:", string(jobResult.Output))
	fmt.Println("		Error:", jobResult.Err)
	// 生成执行日志
	if jobResult.Err != common.ERR_LOCK_ALREADY_REQUIRED {
		// 忽略因为锁被占用的错误
		jobLog = &common.JobLog{
			JobName:      jobResult.ExecuteInfo.Job.Name,
			Command:      jobResult.ExecuteInfo.Job.Command,
			Output:       string(jobResult.Output),
			PlanTime:     jobResult.ExecuteInfo.PlanTime.UnixNano() / 1000 / 1000,
			ScheduleTime: jobResult.ExecuteInfo.RealTime.UnixNano() / 1000 / 1000,
			StartTime:    jobResult.StartTime.UnixNano() / 1000 / 1000,
			EndTime:      jobResult.EndTime.UnixNano() / 1000 / 1000,
		}
		if jobResult.Err != nil {
			jobLog.Err = jobResult.Err.Error()
		} else {
			jobLog.Err = ""
		}
		// 存到 mongodb
		G_logSink.Append(jobLog)
	}
}

// 调度协程
func (scheduler Scheduler) scheduleLoop() {
	var (
		jobEvent      *common.JobEvent
		scheduleAfter time.Duration
		scheduleTimer *time.Timer
		jobResult     *common.JobExecuteResult // 任务执行结果
	)
	// 初始化一次(1秒)
	scheduleAfter = scheduler.TrySchedule()
	// 调度的延迟定时器
	scheduleTimer = time.NewTimer(scheduleAfter)
	// 定时任务 common.Job
	for {
		select {
		case jobEvent = <-scheduler.jobEventChan: // 监听任务变化事件
			// 对*内存中*维护的任务列表做增删改查
			scheduler.handleJobEvent(jobEvent)
		case <-scheduleTimer.C: // 最近的任务到期了
		case jobResult = <-scheduler.jobResultChan: // 监听任务执行结果
			scheduler.handleJobResult(jobResult)
		}
		// 调度一次任务
		scheduleAfter = scheduler.TrySchedule()
		// 重置调度间隔
		scheduleTimer.Reset(scheduleAfter)
	}
}

// 推送任务变化事件
func (scheduler *Scheduler) PushJobEvent(jobEvent *common.JobEvent) {
	scheduler.jobEventChan <- jobEvent
}

// 初始化调度器
func InitScheduler() (err error) {
	G_scheduler = &Scheduler{
		jobEventChan:      make(chan *common.JobEvent, 1000),
		jobPlanTable:      make(map[string]*common.JobSchedulePlan),
		jobExecutingTable: make(map[string]*common.JobExecuteInfo),
		jobResultChan:     make(chan *common.JobExecuteResult, 1000),
	}
	// 启动调度协程
	go G_scheduler.scheduleLoop()
	return
}

// 回传任务执行结果
func (scheduler *Scheduler) PushJobResult(jobResult *common.JobExecuteResult) {
	scheduler.jobResultChan <- jobResult
}
