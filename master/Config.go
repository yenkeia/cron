package master

import (
	"encoding/json"
	"io/ioutil"
)

// 配置结构体
type Config struct {
	ApiPort              int      `json:"apiPort"`
	ApiReadTimeout       int      `json:"apiReadTimeout"`
	ApiWriteTimeout      int      `json:"apiWriteTimeout"`
	EtcdEndpoints        []string `json:"etcdEndpoints"`
	EtcdDialTimeout      int      `json:"etcdDialTimeout"`
	Webroot              string   `json:"webroot"`
	MongodbAuthMechanism string   `json:"mongodbAuthMechanism"`
	MongodbAuthSource    string   `json:"mongodbAuthSource"`
	MongodbUser          string   `json:"mongodbUser"`
	MongodbPass          string   `json:"mongodbPass"`
	MongodbTimeout       int      `json:"mongodbTimeout"`
	MongodbURI           string   `json:"mongodbUri"`
}

var G_config *Config

// 加载配置
func InitConfig(filename string) (err error) {
	var (
		content []byte
		conf    Config
	)
	// 1. 把配置文件读进来
	if content, err = ioutil.ReadFile(filename); err != nil {
		return
	}

	// 2. JSON 反序列化
	if err = json.Unmarshal(content, &conf); err != nil {
		return
	}

	// 3. 赋值单例
	G_config = &conf

	//fmt.Println(conf)

	return
}
