function main(): i32 {
  const a: i32[] = [];
  for (let i = 0; i < 4; i = i + 1) { a.push(i * i); }
  console.log(a[3]);
  return 0;
}
