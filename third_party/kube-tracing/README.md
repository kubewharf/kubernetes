# The kube-tracing client

1. 解决什么问题？
    1. 提升组件可维护性，辅助问题定位和性能优化；
    2. **Overall Insight for Orchestration**：每一次对象状态变更，引发的一些列动作，从k8s到docker，一眼看出block在哪；
    3. 降低学习曲线（组件多且复杂，需要大量运维经验），调度层全局视角；
2. 为什么不用watch所有相关对象的方法？
   object是最终结果，当出现block时，跟直接get没啥区别，而且这是数据流，希望看到**控制流**。因此还需要知道：

   > current_status + action + where + return_value

3. 详细设计

    > https://bytedance.feishu.cn/space/doc/doccnA971QvUU9QAXJLPNg
