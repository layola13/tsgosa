function main(): i32 {
  const a: string[] = [];
  a.push("x");
  a.push("yy");
  console.log(a.length);
  a.unshift("w");
  console.log(a.length);
  const b: number[] = [1, 2, 3];
  b.fill(9);
  console.log(b[0] + b[2]);
  const c = b.with(1, 7);
  console.log(c[1]);
  return 0;
}
