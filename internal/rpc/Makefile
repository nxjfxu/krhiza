.PHONY = all clean

all:
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative krhiza/krhiza.proto
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative kademlia/kademlia.proto

clean:
	- rm krhiza/*.go
	- rm kademlia/*.go
