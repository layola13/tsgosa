function main(): i32 {
  let s1: string = "x";
  let s2: string = "y";
  [s1, s2] = [s2, s1];
  return 0;
}
