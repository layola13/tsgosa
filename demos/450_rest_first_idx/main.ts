function sum(first: i32, ...rest: number[]): i32 {
  let t = first;
  t = t + rest[0];
  t = t + rest.length;
  return t;
}
function main(): i32 {
  console.log(sum(10, 20, 30));
  return 0;
}
