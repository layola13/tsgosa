function main(): i32 {
  const sa: string[] = ["a", "b"];
  sa[0] += "x";
  console.log(sa[0]);
  console.log(sa[1]);
  const i: number = 1;
  sa[i] += "y";
  console.log(sa[1]);
  return 0;
}
