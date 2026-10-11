function main(): i32 {
  const flat: i32[] = [1];
  const [[a]] = flat;
  console.log(a);
  return 0;
}
