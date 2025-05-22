package errno

var (
	// Common errors
	OK                  = &Errno{Code: 0, Message: "OK"}
	InternalServerError = &Errno{Code: 10001, Message: "Internal server error"}
	ErrBind             = &Errno{Code: 10002, Message: "Error occurred while binding the request body to the struct."}
	ErrParam            = &Errno{Code: 10008, Message: "Param error, see doc for more info."}

	ErrValidation = &Errno{Code: 20001, Message: "Validation failed."}
	ErrDatabase   = &Errno{Code: 20002, Message: "Database error."}
	ErrCodeToken   = &Errno{Code: 20003, Message: "code is timeOut."}
	ErrExportNoData  = &Errno{Code: 20004, Message: "no data."}
	// user errors
	ErrEncrypt           = &Errno{Code: 20101, Message: "Error occurred while encrypting the user password."}
	ErrUserNotFound      = &Errno{Code: 20102, Message: "The user was not found."}
	ErrTokenInvalid      = &Errno{Code: 20103, Message: "The token was invalid."}
	ErrPasswordIncorrect = &Errno{Code: 20104, Message: "The password was incorrect."}
	ErrMobile            = &Errno{Code: 20105, Message: "The phone was incorrect."}
	ErrOperate           = &Errno{Code: 20106, Message: "The operate is invalid."}
	ErrNotEnoughMoney    = &Errno{Code: 20107, Message: "The money is not enough."}
	ErrNotMinAmount      = &Errno{Code: 20108, Message: "The money is less than min amount."}
	ErrRedisToken        = &Errno{Code: 20109, Message: "The token is set redis error."}
	ErrTokenIsTimeout    = &Errno{Code: 20110, Message: "The token is timeout."}
	ErrCode              = &Errno{Code: 20111, Message: "The code is timeout."}
	ErrJson              = &Errno{Code: 23001, Message: "The jsonUmaShall is err."}
	Errpassword: REDACTED 23002, Message: "The password or username is err."}

)
