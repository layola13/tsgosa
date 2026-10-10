function main(): i32 {
  const a: i32[] = [1, 4];
  const r: i32[] = a.splice(1, 0, ...[2, 3]);
  console.log(a.length);
  console.log(a[1]);
  console.log(a[2]);
  console.log(r.length);
  return 0;
}
