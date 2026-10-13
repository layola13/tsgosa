function main(): i32 {
  let s1: string = "x";
  let s2: string = "yy";
  [s1, s2] = [s2, s1];
  console.log(s1);
  console.log(s2);
  return 0;
}
