function sum(...rest: number[]): i32 {
  let t: i32 = 0;
  for (const x of rest) {
    t = t + x;
  }
  return t;
}
function main(): i32 {
  console.log(sum(1, 2, 3, 4));
  return 0;
}
