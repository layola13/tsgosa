function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const r: i32[] = a.splice();
  console.log(r.length);
  console.log(a.length);
  return 0;
}
