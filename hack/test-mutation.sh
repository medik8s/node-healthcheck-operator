#!/bin/bash
expectedScore=0.395
echo "starting mutation tests expected score at least: ${expectedScore}"
actualScore=$(go-mutesting controllers | grep "The mutation score is" | awk '{print $5}')
if (( $(echo "$actualScore >= $expectedScore" |bc -l) )); then
  echo "mutation tests passed with score: ${actualScore}"
	exit 0
fi
echo "mutation tests failed expectedScore: ${expectedScore} , actualScore: ${actualScore}"
exit 1