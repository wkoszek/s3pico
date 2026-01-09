implement me pico s3 api in golang.
i want 1 binary run as server "s3pico server" and as client when run with other options ("ls", "put", "get", "sync").

i want files to be stored in individual directory passed as --data argument. by default it should be current directory.

i want some basic s3 operations like get, put, list to be supported, and whatever else is easy for the server.
for client i want basic ops too.

for now paths should be authentication mechanism: unique paths will have short UUIDs and will be for different projects.
make it look like 1 bucket for now, but the paths will be unique.

put everything in 1 .go file.
add makefile for building on local box, and also remote amd64 linux box with musl cross-compiler.
'make ddd' should ssh to "ovh" box, and mv binary to .old binary, and then compile linux binary and deploy it via streaming zstd compressed binary via ssh to remove apps/s3pico/ directory.

