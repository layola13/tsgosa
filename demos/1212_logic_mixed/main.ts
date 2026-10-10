function main(): i32 {
  console.log(0 && 1 || 2);
  console.log(1 || 0 && 0);
  console.log(!(0 && 1));
  return 0;
}
