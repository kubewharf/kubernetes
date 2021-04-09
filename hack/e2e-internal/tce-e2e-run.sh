#!/bin/bash

e2e_server="server.e2e-tce.byted.org"
#e2e_server="127.0.0.1:8080"

echo "::add-message :: branch: ${CI_HEAD_BRANCH}"
echo "::add-message :: commit id: ${CI_EVENT_CHANGE_SOURCE_SHA}"

echo "==============call e2e test ==========================="
# 从git message中获取e2e参数
E2E_MESSAGE=`git log --pretty=format:"%b" -1 | grep "\[E2E-Test\]"`
E2E_PARAMS=`echo $E2E_MESSAGE | awk -F "E2E-Test\]" '{print $2}'`

# 设置默认参数
if [[ "${E2E_PARAMS}x" == "x" ]];then
  E2E_PARAMS='--ginkgo.focus=\[sig-apps\] Deployment --ginkgo.skip=deployment should support proportional scaling'
fi

# 对"\"进行转义
E2E_PARAMS=${E2E_PARAMS//"\\"/"\\\\"}

echo ":add-message ::E2E params: $E2E_PARAMS"

# 请求e2e server，创建e2e job
req="{
\"spec\": {
		\"branch\": \"${CI_HEAD_BRANCH}\",
		\"commit\": \"${CI_EVENT_CHANGE_SOURCE_SHA}\",
		\"e2eparams\": \"${E2E_PARAMS}\",
		\"type\": \"k8s\"
	}
}
"
echo requst body: $req

respCode=`curl -w "%{http_code}" $e2e_server/api/v1/e2ejobs -X POST -s -o /tmp/e2e-resp -d "$req"`
resp=`cat /tmp/e2e-resp`
echo "create e2ejob resp code: ${respCode}, body: ${resp}"
if [[ "${respCode}x" != "200x" ]]; then
  echo ::add-message :: create e2e job failed
  exit 1
fi

jobName=`echo $resp | grep -Eo "\"name\":\"[a-z0-9-]*" | awk -F ":\"" '{print $2}' | tr -d " "`

echo "============== wait e2e result ============================="

# 等待e2ejob中的logurl，需要等待scm编译、部署集群完成后才会创建job运行e2e
for i in {0..50};do
  echo check count: $i
  resp=`curl $e2e_server/api/v1/e2ejobs/${jobName} -s`
  logurlLine=`echo $resp | python -m json.tool | grep "\"logurl\""`
  if [[ "${logurlLine}x" != "x" ]]; then
    logurl=`echo $logurlLine | awk -F '"' '{print $4}'`
  fi

  echo logurl: $logurl
  if [[ "${logurl}x" != "x" ]]; then
    echo job logurl: $logurl
    break
  fi
  sleep 30
done

# print e2e log
curl http://$e2e_server/api/v1/e2ejobs/log/${jobName}?follow=true

# print result
for i in {0..10};do
  resp=`curl $e2e_server/api/v1/e2ejobs/${jobName}`
  status=`echo $resp | python -m json.tool | grep "\"phase\"" | awk -F '"' '{print $4}'`
  echo  status: $status
  if [[ "${status}x" == "Successx" ]]; then
    echo ::add-message :: e2e job success
    exit 0
  fi
  if [[ "${status}x" == "Failedx" ]]; then
    echo ::add-message :: e2e job failed
    exit 1
  fi
  sleep 5
done