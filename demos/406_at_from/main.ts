function main(): i32 {
  const a: i32[] = [1, 2, 3];
  console.log(a.at(0) + a.at(-1));
  const b: i32[] = Array.from([4, 5]);
  console.log(b.length + b[1]);
  return 0;
}
