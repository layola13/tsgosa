function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  console.log(a.toSpliced(1, 2).length);
  console.log(a.length);
  return 0;
}
