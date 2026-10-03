function main(): i32 {
  const a: number[] = [5, 1, 9];
  console.log(a.indexOf(9));
  console.log(a.lastIndexOf(1));
  console.log(a.includes(5));
  console.log(a.at(2));
  return 0;
}
