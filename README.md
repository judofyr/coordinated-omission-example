# Example of Coordinated Omission

In this repo there is a very simple HTTP server written in Go.
Let's not look at the code right now, but instead run a benchmark to try to understand its performance.

```
go run main.go
```

And then use `wrk` to run a benchmark:

```
$ wrk --duration 30s --connections 10 --latency --timeout 3s http://localhost:8989
Running 30s test @ http://localhost:8989
  2 threads and 10 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency   119.50ms  375.86ms   2.00s    91.25%
    Req/Sec    44.83k     7.35k   53.38k    91.36%
  Latency Distribution
     50%   90.00us
     75%  120.00us
     90%  350.82ms
     99%    1.83s
  2182301 requests in 30.03s, 245.58MB read
Requests/sec:  72660.52
Transfer/sec:      8.18MB
```

- 70k requests per second is not bad for only 10 concurrent requests.
- The 75% percentile looks great, but after that things get worse.
- The maximum latency is all the way at 2s, but maybe that's because it's running at full capacity?
  We don't expect 70k requests per second any way, so we should be good, right?

So what does this server actually do?
Have a look in `main.go` and you will find this piece of code:

```go
go func() {
    for {
        time.Sleep(8*time.Second)
        lock.Lock()
        time.Sleep(2*time.Second)
        lock.Unlock()
    }
}()
```

- For 8 second the system will run as normal.
- After that it will take out a lock which stalls _every_ request.
- We keep the lock for 2 second.
- And then we release the lock again.

Our system is **completely unresponsive** for _two seconds_.
We're doing _nothing_ 20% of the time.
What a horrible system!
Within the 20%-period we expect request to take one second on average:
A request which comes early in the period needs to wait two seconds and a request which comes at the end waits just a few milliseconds.
The average is in the middle at one second.
However, the percentiles show that only 10% of the requests took more than half a millisecond?
Why didn't our benchmarking catch this more clearly?

This is a case of "coordinated omission", a term coined by Gil Tene.
The problem is that our benchmarking tool follows the pace of the server.
Once the server starts slowing down we also start sending it fewer requests.
In periods of healthy behavior we're able to process _a lot_ of requests and this heavily skews the average and percentiles.
The percentiles are around the _requests_, not time spans.
90% of the requests are faster than ~1ms, but that's because the vast majority of the requests are being sent in the "good" period.

Real-life systems very often exhibit "good" and "bad" periods:
Either the system is running as normal, or it's under high load and struggling.
If your benchmark is exercising both periods then coordinated omission will underreport what actually happens during the "bad" period.

What's a better way of benchmarking this?
Luckily Gil Tene has us covered:
He's written [wrk2](https://github.com/giltene/wrk2) which adjusts for coordinated omission:

```
$ $ ./wrk --duration 30s --connections 10 --latency --timeout 3s --rate 70k http://localhost:8989
Running 30s test @ http://localhost:8989
  2 threads and 10 connections
  Thread calibration: mean lat.: 120.802ms, rate sampling interval: 13ms
  Thread calibration: mean lat.: 121.160ms, rate sampling interval: 11ms
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     1.53s   530.66ms   2.96s    66.49%
    Req/Sec    35.29k    18.05k   62.90k    79.67%
  Latency Distribution (HdrHistogram - Recorded Latency)
 50.000%    1.48s
 75.000%    1.85s
 90.000%    2.31s
 99.000%    2.92s
 99.900%    2.95s
 99.990%    2.96s
 99.999%    2.96s
100.000%    2.96s
```

These numbers clearly shows us that this system is _far_ from healthy.
Even the 50% percentile is severely impacted.
The way wrk2 works is that you give it a target request rate (notice that we had to pass `--rate 70k` as a parameter) and it tries to send a constant rate of requests.
wrk was able to send 70k requests per second on average so this should be fine?
Well, as we can see from the numbers here we have _terrible_ latency, across all percentiles, at 70k requests per second.

The way wrk2 works is that it sends requests as usual, but when it detects that the server isn't able to keep up with the rate it will adjust the subsequent numbers.
From wrk2's README:

> The model I chose for avoiding Coordinated Omission in wrk2 combines the use of constant throughput load generation with latency measurement that takes the intended constant throughput into account. Rather than measure response latency from the time that the actual transmission of a request occurred, wrk2 measures response latency from the time the transmission should have occurred according to the constant throughput configured for the run. When responses take longer than normal (arriving later than the next request should have been sent), the true latency of the subsequent requests will be appropriately reflected in the recorded latency stats.

A diagram might make this easier to understand:

```
Request rate:       |      |      |      |      |      |
Measured latency:   1-->   2-->   3-------->4------->5-->6-->
Adjusted latency:   1-->   2-->   3-------->
                                         4---------->
                                                5------->
                                                       6---->
```

- The bars at the top represents when we _want_ the requests to fire.
  They run at a constant rate.
- "Measured latency" is how we actually send requests to the server.
  We're using a single connection so we can only send one request at a time.
  "Adjusted latency" is how wrk2 will _actually_ report the latency.
- The first two requests were completed swiftly.
- The third request took so long that we weren't able to send the fourth request at the right time. 
  The _server_ was able to process the fourth request in "7 dashes", but from the _client's_ perspective it took "10 dashes" to complete it.
- And most notably: Even though the fifth and sixth requests were processed quickly by the server, their adjusted latency becomes worse due to the slow processing of the third/fourth.

Another way of thinking about this is that you have a web browser which can only have one open connection to the server and we want to send a constant rate of requests.
Obviously we need to include "waiting until the connection becomes available" if we want to measure the time it takes to complete a request.
Who cares that it took one minute to buy a concert ticket if you were in the line for hours?

By adjusting for coordinated omission we see that "70k requests per second" will in real life give a much higher latency than what wrk originally suggested.

And even more telling: What if we attempt a much lower amount of requests per second?

```
$ ./wrk --duration 30s --connections 10 --latency --timeout 3s --rate 10k http://localhost:8989
Running 30s test @ http://localhost:8989
  2 threads and 10 connections
  Thread calibration: mean lat.: 223.592ms, rate sampling interval: 2205ms
  Thread calibration: mean lat.: 223.570ms, rate sampling interval: 2207ms
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency   227.00ms  500.75ms   2.00s    85.63%
    Req/Sec     5.00k     1.47k    7.28k    55.56%
  Latency Distribution (HdrHistogram - Recorded Latency)
 50.000%    1.25ms
 75.000%    1.86ms
 90.000%    1.12s
 99.000%    1.92s
 99.900%    1.99s
 99.990%    2.00s
 99.999%    2.00s
100.000%    2.00s
```

At 10k requests per second we're still heavily struggling with 10% of the requests.