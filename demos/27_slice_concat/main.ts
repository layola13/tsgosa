function main(): i32 {
  const a: number[] = [1, 2, 3, 4, 5];
  const s = a.slice(1, 4);
  console.log(s.length + s[0] + s[2]);
  const c = a.concat([10, 20]);
  console.log(c.length + c[5]);
  return 0;
}
