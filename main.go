package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Script struct {
	ID       string
	Executor string
	Extra    string
	Service  string
	Name     string
	Args     []string
}

func InitScript(id, scriptExtra, srv, name string, args ...string) *Script {
	return &Script{
		ID:       id,
		Extra:    scriptExtra,
		Service:  srv,
		Executor: "python3",
		Name:     name,
		Args:     args,
	}
}

type Result struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Service string `json:"srv"`
	Extra   string `json:"extra"`
	Answer  string `json:"answer"`
}

type Request struct {
	ID      string   `json:"id"`
	Service string   `json:"srv"`
	Extra   string   `json:"extra"`
	Script  string   `json:"script"`
	Args    []string `json:"args"`
}

func (s *Script) Run(r, e chan Result) {
	args := append([]string{s.Name}, s.Args...)
	out, err := exec.Command(s.Executor, args...).Output()
	if err != nil {
		e <- Result{s.ID, s.Args[0], s.Service, "error", err.Error()}
	} else {
		r <- Result{s.ID, s.Args[0], s.Service, s.Extra, string(out)}
	}
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Panicf("%s: %s", msg, err)
	}
}

func LoadScripts(scriptDir string) {
	endpoint := os.Getenv("MINIO_HOST")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY")
	secretAccessKey := os.Getenv("MINIO_SECRET_KEY")
	useSSL := false

	// Initialize minio client object.
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalln(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	objectCh := minioClient.ListObjects(ctx, "scripts", minio.ListObjectsOptions{
		Prefix:    "",
		Recursive: true,
	})
	for object := range objectCh {
		if object.Err != nil {
			log.Println(object.Err)
			continue
		}
		err := minioClient.FGetObject(context.Background(), "scripts", object.Key, scriptDir+object.Key, minio.GetObjectOptions{})
		if err != nil {
			log.Println(err)
		}
	}
}

func Execute(requests []Request) []Result {
	scriptDir := "scripts/"
	errors := make(chan Result)
	results := make(chan Result)
	LoadScripts(scriptDir)

	for _, r := range requests {
		script := InitScript(r.ID, r.Extra, r.Service, scriptDir+r.Script, r.Args...)
		go script.Run(results, errors)
	}
	var answers []Result
	for range requests {
		select {
		case r := <-results:
			answers = append(answers, r)
			log.Printf("%s:%s:%s return '%s'", r.ID, r.Service, r.Action, r.Answer)
		case e := <-errors:
			answers = append(answers, e)
			log.Printf("%s:%s:%s error '%s'", e.ID, e.Service, e.Action, e.Answer)
		}
	}
	return answers
}

func main() {
	ctx := context.Background()
	url := fmt.Sprintf(
		"amqp://%s:%s@%s:5672/",
		os.Getenv("RABBITMQ_USER"),
		os.Getenv("RABBITMQ_PASS"),
		os.Getenv("RABBITMQ_HOST"),
	)
	conn, err := amqp.Dial(url)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"runner", // name
		false,    // durable
		false,    // delete when unused
		false,    // exclusive
		false,    // no-wait
		nil,      // arguments
	)
	failOnError(err, "Failed to declare a queue")

	err = ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	failOnError(err, "Failed to set QoS")

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	var forever chan struct{}

	go func() {
		for d := range msgs {
			var requests []Request
			err = json.Unmarshal(d.Body, &requests)
			if err != nil {
				log.Println(err)
			}
			response := Execute(requests)
			j, err := json.Marshal(response)
			if err != nil {
				log.Printf("Error: %s", err.Error())
			}
			err = ch.PublishWithContext(
				ctx,
				"",        // exchange
				d.ReplyTo, // routing key
				false,     // mandatory
				false,     // immediate
				amqp.Publishing{
					ContentType:   "text/plain",
					CorrelationId: d.CorrelationId,
					Body:          j,
				})
			failOnError(err, "Failed to publish a message")

			d.Ack(false)
		}
	}()

	log.Printf(" [*] Awaiting RPC requests")
	<-forever
}
