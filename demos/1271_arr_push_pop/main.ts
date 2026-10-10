function main(): i32 {
  const a: i32[] = [1, 2];
  a.push(3);
  console.log(a.length);
  const v: i32 = a.pop();
  console.log(v);
  console.log(a.length);
  return 0;
}
