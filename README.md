# Redis_Lock
本项目是使用Redis实现的轮询形分布式锁，通过在redis中缓存key，来判断该锁是否被使用，通过看门狗策略来进行续期，避免锁提前过期导致临界资源暴露。如果使用redis集群，需要通过红锁解决redis弱一致性的问题。