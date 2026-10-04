function main(): i32 {
  const a: number[] = [1, 2, 3, 4, 5];
  const k: i32 = 2;
  const b: number[] = a.slice(k).concat(a.slice(0, k));
  console.log(b[0], b[1], b[4]);
  return 0;
}