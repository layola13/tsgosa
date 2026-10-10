function sum(a: i32[]): i32 {
  let t = 0;
  for (const v of a) { t += v; }
  return t;
}
function main(): i32 {
  console.log(sum([1, 2]));
  console.log(sum([3, 4, 5]));
  return 0;
}
