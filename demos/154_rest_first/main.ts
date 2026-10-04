function f(first: i32, ...rest: number[]): i32 {
  return first + rest.length;
}
function main(): i32 {
  console.log(f(10, 1, 2, 3));
  return 0;
}