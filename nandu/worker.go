package nandu

import (
	"container/list"
	"github.com/Jiajun-Fan/nandu/common"
	"github.com/Jiajun-Fan/nandu/util"
	"github.com/jinzhu/gorm"
	"net/http"
	"time"
)

const (
	kNanduConfigFile = "nandu.json"
)

type Worker struct {
	info       *NanduInfo
	project    string
	retryCount uint
	retryMax   uint
	tasksets   map[string]*TaskSet
	clients    map[string]*http.Client
	databases  map[string]NanduDB
	localTask  list.List
}

func (worker *Worker) GetDB(name string) *gorm.DB {
	database, ok := worker.databases[name]
	if !ok {
		util.Fatal("can't find database")
	}
	return database.DB
}

func (worker *Worker) GetInfo() *NanduInfo {
	return worker.info
}

func (worker *Worker) GetClient(name string) *http.Client {
	client, ok := worker.clients[name]
	if !ok {
		util.Fatal("can't find client")
	}
	return client
}

func (worker *Worker) Push(task *common.Task) *common.Task {
	task.Token = worker.info.Server.Token
	task.Project = worker.project
	r, err := util.HttpPostJSON(worker.info.Server.AddrPush(), task)
	if err != nil {
		util.Error("failed to push task, %s\n", err.Error())
		return nil
	}

	resp := new(common.CommonResponse)
	err = util.HttpResponseUnmarshalJSON(resp, r, http.StatusOK)

	if err != nil {
		util.Error("failed to push task, %s\n", err.Error())
		return nil
	} else {
		if resp.Task != nil {
			util.Debug(resp.Task.PushLog())
		}
	}
	return resp.Task
}

func (worker *Worker) Pop() *common.Task {
	r, err := util.HttpPostJSON(worker.info.Server.AddrPop(), &common.Worker{worker.info.Server.Token, worker.project})
	if err != nil {
		util.Error("failed to pop task, %s\n", err.Error())
		return nil
	}

	resp := new(common.CommonResponse)
	err = util.HttpResponseUnmarshalJSON(resp, r, http.StatusOK)

	if err != nil {
		util.Error("failed to pop task, %s\n", err.Error())
		return nil
	} else {
		if resp.Task != nil {
			util.Debug(resp.Task.PopLog())
		}
	}

	return resp.Task
}

func (worker *Worker) PushLocal(task *common.Task) {
	worker.localTask.PushBack(task)
}

func (worker *Worker) PopLocal() *common.Task {
	if worker.localTask.Len() == 0 {
		return nil
	}
	return worker.localTask.Remove(worker.localTask.Front()).(*common.Task)
}

func (worker *Worker) registerDatabase() {
	worker.databases = make(map[string]NanduDB)
	for i := range worker.info.Databases {
		dinfo := worker.info.Databases[i]
		database := NewDatabase(dinfo.DbType, dinfo.ConnectStr)
		if database == nil {
			continue
		}

		if _, ok := worker.databases[dinfo.Name]; !ok {
			worker.databases[dinfo.Name] = NanduDB{database, dinfo.Init}
		} else {
			util.Warning("double register database %s\n", dinfo.Name)
		}
	}
}

func (worker *Worker) registerClients() {
	worker.clients = make(map[string]*http.Client)
	for i := range worker.info.Oauths {
		oinfo := worker.info.Oauths[i]
		oauth := NewOauth(oinfo.AppKey, oinfo.AppSecret, oinfo.Token, oinfo.Secret)
		if oauth == nil {
			continue
		}

		if _, ok := worker.clients[oinfo.Name]; !ok {
			worker.clients[oinfo.Name] = oauth
		} else {
			util.Warning("double register client for oauth %s\n", oinfo.Name)
		}
	}
}

func (worker *Worker) TaskSet(name string) *TaskSet {
	if worker.tasksets == nil {
		worker.tasksets = make(map[string]*TaskSet)
	}
	taskset := NewTaskSet(name, worker)
	worker.tasksets[name] = taskset
	return taskset
}

func (worker *Worker) Model(name string, model interface{}) *Worker {
	database, ok := worker.databases[name]
	if !ok {
		util.Fatal("can't find database %s\n", name)
	}
	if database.Init {
		database.DB.CreateTable(model)
	}
	return worker
}

func (worker *Worker) checkParsers() {
	for i := range worker.tasksets {
		if worker.tasksets[i].parser == nil {
			util.Fatal("missing parser of tasksets %s\n", worker.tasksets[i].Name)
		}
	}
}

func (worker *Worker) validate() {
	worker.checkParsers()
}

func (worker *Worker) Run() {

	worker.validate()

	util.Info("'%s' started\n", worker.project)

	for {
		task := worker.PopLocal()
		if task == nil {
			task = worker.Pop()
		}
		if task != nil {
			worker.retryCount = 0
			if taskset, ok := worker.tasksets[task.TaskSet]; !ok {
				util.Error("can't find taskset %s\n", task.TaskSet)
				continue
			} else {
				data := taskset.Fetch(task)
				if data != nil {
					taskset.Parse(task, data)
				}
			}
		} else {
			worker.retryCount += 1
			if worker.retryCount >= worker.retryMax {
				break
			}
			util.Info("sleep 1 second, ( %d | %d )\n", worker.retryCount, worker.retryMax)
			time.Sleep(time.Second)
		}
	}

	util.Info("'%s' exit\n", worker.project)
}

func NewWorker() *Worker {
	worker := new(Worker)
	worker.retryCount = 0
	worker.retryMax = 10
	info, err := NewNanduInfo(kNanduConfigFile)
	if err != nil {
		util.Fatal("failed to load config file %s\n", err.Error())
	}
	worker.info = info
	worker.project = info.Project
	worker.registerClients()
	worker.registerDatabase()
	return worker
}
