function main(): i32 {
  const s: string = "hello";
  console.log(s.length);
  console.log(s + " world");
  console.log(s.indexOf("ll"));
  const csv: string = "a,b,c";
  console.log(csv.indexOf(","));
  console.log(csv.slice(2, 5));
  return 0;
}
