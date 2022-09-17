# Example of Coordinated Omission

In this repo there is a very simple HTTP server written in Go.
Let's not look at the code right now, but instead run a benchmark to try to understand its performance.

```
go run main.go
```

And then use `wrk` to run a benchmark:

```
$ wrk --duration 30s --connections 50 --latency http://localhost:8989
Running 30s test @ http://localhost:8989
  2 threads and 50 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     1.63ms   41.73ms   2.00s    99.87%
    Req/Sec    63.90k     8.04k   77.52k    90.84%
  Latency Distribution
     50%  297.00us
     75%  372.00us
     90%  448.00us
     99%    1.16ms
  3343896 requests in 30.03s, 376.30MB read
  Socket errors: connect 0, read 0, write 0, timeout 99
Requests/sec: 111362.40
Transfer/sec:     12.53MB
```

At first this looks pretty okay:

- 110k requests per second is not bad!
- We have an average latency _and_ 99% percentile at 1ms.
- There's a maximum latency of 2s and some timeouts.
  This is a bit concerning, but we're stress testing the system so we expect higher latency at the tail, right?
  In production we don't expect to run at 110k requests per second so this will be fine.
  Or?

So what does this server actually do?
Have a look in `main.go` and you will find this piece of code:

```go
go func() {
    for {
        time.Sleep(10*time.Second)
        lock.Lock()
        time.Sleep(2*time.Second)
        lock.Unlock()
    }
}()
```

- For 10 second the system will run as normal.
- After that it will take out a lock which stalls _every_ request.
- We keep the lock for 2 second.
- And then we release the lock again.

Our system is **completely unresponsive** for _two seconds_.
We're doing _nothing_ 20% of the time.
What a horrible system!
Within the 20%-period we expect request to take one second on average:
A request which comes early in the period needs to wait two seconds and a request which comes at the end waits just a few milliseconds.
The average is in the middle at one second.
Why didn't our benchmarking catch this more clearly?
Why is both our 90% and 99% percentile so great?

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
$ ./wrk --duration 30s --connections 50 --latency --rate 100k http://localhost:8989
Running 30s test @ http://localhost:8989
  2 threads and 50 connections
  Thread calibration: mean lat.: 658.052ms, rate sampling interval: 3422ms
  Thread calibration: mean lat.: 632.471ms, rate sampling interval: 3418ms
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency   621.23ms  727.10ms   2.01s    75.24%
    Req/Sec    47.31k    15.41k   68.09k    60.00%
  Latency Distribution (HdrHistogram - Recorded Latency)
 50.000%  179.46ms
 75.000%    1.34s
 90.000%    1.80s
 99.000%    1.99s
 99.900%    2.00s
 99.990%    2.01s
 99.999%    2.01s
100.000%    2.01s
```

These numbers clearly shows us that this system is not healthy.
The way wrk2 works is that you give it a target request rate (notice that we had to pass `--rate 100k` as a parameter).
It sends requests as usual, but when it detects that the server isn't able to keep up with the rate it will adjust the subsequent numbers.
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
