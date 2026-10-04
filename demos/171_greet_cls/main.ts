class Greeter {
  name: string;
  constructor(name: string) {
    this.name = name;
  }
  hello(): string {
    return this.name;
  }
}
function main(): i32 {
  const g = new Greeter("hi");
  console.log(g.hello());
  return 0;
}