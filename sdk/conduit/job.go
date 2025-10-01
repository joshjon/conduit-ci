package conduit

import (
	"strconv"
)

const jobActivityPrefix = "job.activity."

type JobExecutionID int32

func (i JobExecutionID) Int32() int32 {
	return int32(i)
}

func (i JobExecutionID) String() string {
	return strconv.Itoa(int(i))
}

func (i JobExecutionID) ActivityID() string {
	return jobActivityPrefix + i.String()
}

func (i JobExecutionID) IsEmpty() bool {
	return i == 0
}
