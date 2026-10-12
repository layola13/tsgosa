function bump(): string { console.log("bump"); return "B"; }
function main(): i32 {
  const sa: string[] = ["a", "b"];
  sa[5] += bump();
  sa[0] += bump();
  console.log(sa[0]);
  return 0;
}
