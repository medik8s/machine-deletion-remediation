#!/bin/bash
expectedScore=0.75 #Current fails are due to log mutations (not verifying proper logs in tests)
echo "starting mutation tests expected score at least: ${expectedScore}"
testsOutput=$(go-mutesting controllers)
actualScore=$(echo "$testsOutput" | grep "The mutation score is" | awk '{print $5}')
if (( $(echo "$actualScore >= $expectedScore" |bc -l) )); then
  echo "mutation tests passed with score: ${actualScore}"
	exit 0
fi
echo "$testsOutput"
echo "mutation tests failed expectedScore: ${expectedScore} , actualScore: ${actualScore}"
exit 1