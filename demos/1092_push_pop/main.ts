function main(): i32 {
  const a: i32[] = [1];
  a.push(2, 3, 4);
  console.log(a.length);
  console.log(a[3]);
  const b = a.pop();
  console.log(b ?? -1);
  return 0;
}
