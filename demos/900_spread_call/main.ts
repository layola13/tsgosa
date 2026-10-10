function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
  const args: i32[] = [3, 4];
  console.log(add(...args));
  return 0;
}
