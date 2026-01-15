# peer_checker.go
Yggdrasil [peers](https://github.com/yggdrasil-network/public-peers) checker. 

Script https://github.com/zhoreeq/peer_checker.py rewritten using Golang.

Usage:
```
go run . ../public-peers/
```

or
```
go build
./peer_checker ../public-peers
```

or export URIs only in JSON format
```
# first 10 fastest alive peers
go run . --json ~/public-peers/ | jq '.alive[:10] | map(.uri)'

# first 5 fastest alive peers for Europe
go run . --json ~/public-peers/ | jq '.alive | map(select(.region == "europe"))[:5] | map(.uri)'

# combined
go run . --json ~/public-peers/ | \
jq '.alive as $p | 
    (
      $p[:5] + 
      ($p | map(select(.region == "europe"))[:5]) + 
      ($p | map(select(.country == "russia.md"))[:5])
    ) 
    | map(.uri) 
    | unique'
```

