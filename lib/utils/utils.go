package utils

func GetOrElse(value interface{}, err error) func(defaultValue interface{}) interface{} {
	return func(defaultValue interface{}) interface{} {
		if err != nil {
			return defaultValue
		} else {
			return value
		}
	}
}