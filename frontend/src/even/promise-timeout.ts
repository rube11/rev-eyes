export function withTimeout<T>(
  work: PromiseLike<T>,
  timeoutMilliseconds: number,
  message: string,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timeout = setTimeout(() => {
      reject(new Error(message))
    }, timeoutMilliseconds)

    Promise.resolve(work).then(
      (value) => {
        clearTimeout(timeout)
        resolve(value)
      },
      (error: unknown) => {
        clearTimeout(timeout)
        reject(error)
      },
    )
  })
}
