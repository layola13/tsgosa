function main(): i32 {
  const a: i32[] = [];
  console.log(a.pop() ?? -1);
  console.log(a.length);
  const s: i32[] = [];
  console.log(s.shift() ?? -2);
  console.log(s.length);
  return 0;
}
