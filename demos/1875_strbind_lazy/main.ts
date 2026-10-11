let n: i32 = 0;
function hit(): string {
  n = n + 1;
  return "d";
}
function main(): i32 {
  const sa: string[] = ["x"];
  const [a = hit(), b = hit()] = sa;
  console.log(a);
  console.log(b);
  console.log(n);
  return 0;
}
