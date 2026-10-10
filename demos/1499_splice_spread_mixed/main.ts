function main(): i32 {
  const a: i32[] = [1, 5];
  const b: i32[] = [2, 3];
  const r: i32[] = a.splice(1, 0, 9, ...b, 4);
  console.log(a.length);
  console.log(a[1]);
  console.log(a[2]);
  console.log(a[4]);
  console.log(r.length);
  return 0;
}
