function main(): i32 {
  const a: number[] = [];
  for (let i: i32 = 0; i < 8; i++) {
    a.push(2 ** i);
  }
  console.log(a.length, a[0], a[7]);
  return 0;
}