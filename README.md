# tgik-controller

Example controller for TGIK talk.

Covers:
- Controllers
- Getters
- Informers
- Listers
- Cache Synchronisation
- Rate Limiting Queues & Workers

## Videos
This sample repository was developed and explained across three episodes of the [TGI Kubernetes](https://www.youtube.com/watch?v=9YYeE-bMWv8&list=PLvmPtYZtoXOENHJiAQc6HmV2jmuexKfrJ) YouTube Series.
- [TGI Kubernetes 007: Building a Controller](https://www.youtube.com/watch?v=8Xo_ghCIOSY)
- [TGI Kubernetes 008: Continuing the Controller](https://www.youtube.com/watch?v=fWkK-zsFtlU)
- [TGI Kubernetes 009: Finishing the Controller](https://www.youtube.com/watch?v=wqhKCiGsf1Y)


## Ideas/code stolen from...
This repo vendors in client-go along with all of the appropriate dependencies.  The procedure followed is described by @ncdc in this [blog post](https://blog.heptio.com/straighten-out-your-kubernetes-client-go-dependencies-heptioprotip-8baeed46fe7d).

The docker build makefile and such was taken from https://github.com/thockin/go-build-template.
