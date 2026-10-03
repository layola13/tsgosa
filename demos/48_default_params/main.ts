function greet(name: string = "hi"): string {
  return name + "!";
}
function main(): i32 {
  console.log(greet());
  return 0;
}
