.PHONY = build install rpc clean
OUT = out

build:
	- mkdir out
	make gen
	go build -o $(OUT) github.com/nxjfxu/krhiza/cmd/krhiza-client github.com/nxjfxu/krhiza/cmd/krhiza-server

install:
	make gen
	go install github.com/nxjfxu/krhiza/cmd/krhiza-client github.com/nxjfxu/krhiza/cmd/krhiza-server

gen:
	make -C internal/rpc all

clean:
	make -C internal/rpc clean
	- rm -rf out

