package arrayutil

func SafeSlice[T any](slice []T, max int) []T {
	if len(slice) < max {
		return slice
	}
	return slice[:max]
}

func StringInSlice(target string, list []string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func RemoveAtIndex(slice []string, index int) []string {
	if index < 0 || index >= len(slice) {
		return slice // 越界则不修改
	}
	return append(slice[:index], slice[index+1:]...)
}

func SafeSliceRemove(slice []string, target string) []string {
	for i, item := range slice {
		if item == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice // 未找到则返回原 slice
}
